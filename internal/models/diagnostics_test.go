package models

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeListener is the private web listener of a managed child: it records the
// readiness read it received and answers like the original front page.
func fakeListener(t *testing.T, status int, body string, cookieName *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookieName != nil {
			*cookieName = r.Header.Get("Cookie")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
}

func fakeProcess(t *testing.T, server *httptest.Server, webKey string) *nativeProcess {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sleepBinary(t), "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-waited })
	process := &nativeProcess{
		cmd: cmd, done: make(chan error, 1),
		webURL: server.URL, webHost: parsed.Host, webKey: webKey,
		ports: nativePorts{Web: 4242}, client: server.Client(),
	}
	process.pid, process.startedAt = cmd.Process.Pid, time.Now().UTC()
	return process
}

// sleepBinary is the real executable, not a shell: killing the managed child must
// not leave an orphan grandchild holding the fixture.
func sleepBinary(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatalf("this fixture needs a real sleep executable: %v", err)
	}
	return path
}

func decodeDiagnostics(t *testing.T, raw json.RawMessage) nativeDiagnostics {
	t.Helper()
	var report nativeDiagnostics
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("diagnostics JSON is invalid: %v (%s)", err, raw)
	}
	return report
}

// A live probe is a real private read; the report must keep the private key, the
// child environment and the loopback address out of everything it publishes.
func TestDiagnosticsProbeIsRealAndKeepsSecretsOut(t *testing.T) {
	const canaryKey = "CANARY-private-web-key-must-not-be-published"
	const canaryEnv = "CANARY-child-environment-must-not-be-published"
	t.Setenv("WORKBENCH_DIAGNOSTICS_CANARY", canaryEnv)
	var cookie string
	server := fakeListener(t, http.StatusOK, "<title>magpie</title>", &cookie)
	defer server.Close()
	process := fakeProcess(t, server, canaryKey)
	process.record("start", "managed process started")

	raw, err := json.Marshal(process.diagnostics(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	report := decodeDiagnostics(t, raw)
	if report.State != "ready" || !report.Healthy {
		t.Fatalf("ready module reported %q healthy=%v (%s)", report.State, report.Healthy, raw)
	}
	if report.PID != process.pid || report.PID == 0 || report.Probes != 1 || report.ProbeFailures != 0 || report.ReadyAt == nil {
		t.Fatalf("report does not reflect the real process and probe: %s", raw)
	}
	if len(report.Events) != 2 || report.Events[0].Event != "start" || report.Events[1].Event != "probe_ok" {
		t.Fatalf("lifecycle log = %+v, want start + probe_ok", report.Events)
	}
	if !strings.HasPrefix(cookie, "magpie_web_4242=") {
		t.Fatalf("readiness read did not use the private web cookie: %q", cookie)
	}
	published := string(raw)
	for _, canary := range []string{canaryKey, canaryEnv, "magpie_web_", server.URL, "WORKBENCH_DIAGNOSTICS_CANARY"} {
		if strings.Contains(published, canary) {
			t.Fatalf("diagnostics published %q: %s", canary, published)
		}
	}
}

// A module that answers with an error is not healthy, and the failure is recorded
// once per retry with a bounded, address-free detail.
func TestDiagnosticsReportFailureAndRetries(t *testing.T) {
	server := fakeListener(t, http.StatusInternalServerError, "boom", nil)
	defer server.Close()
	process := fakeProcess(t, server, "private-key")

	for attempt := 1; attempt <= 2; attempt++ {
		report := decodeDiagnostics(t, json.RawMessage(mustMarshal(t, process.diagnostics(context.Background()))))
		if report.Healthy || report.State != "unready" {
			t.Fatalf("attempt %d reported %q healthy=%v, want unready", attempt, report.State, report.Healthy)
		}
		if report.ProbeFailures != attempt || report.Probes != attempt {
			t.Fatalf("attempt %d counters = %d/%d, want %d failures of %d probes", attempt, report.ProbeFailures, report.Probes, attempt, attempt)
		}
		if !strings.Contains(report.Detail, "HTTP 500") || strings.Contains(report.Detail, server.URL) {
			t.Fatalf("attempt %d detail = %q, want a bounded HTTP code without the listener address", attempt, report.Detail)
		}
		if last := report.Events[len(report.Events)-1]; last.Event != "probe_failed" {
			t.Fatalf("attempt %d log = %+v, want a probe_failed line", attempt, report.Events)
		}
	}
}

// A dead child is reported as stopped with its real exit status, and no probe is
// invented for it.
func TestDiagnosticsReportsRealExit(t *testing.T) {
	process := &nativeProcess{}
	process.cmd = exec.Command("/bin/sh", "-c", "exit 7")
	if err := process.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process.pid, process.startedAt = process.cmd.Process.Pid, time.Now().UTC()
	process.done = make(chan error, 1)
	process.record("start", "managed process started")
	go process.watchExit()
	<-process.done

	report := decodeDiagnostics(t, json.RawMessage(mustMarshal(t, process.diagnostics(context.Background()))))
	if report.State != "stopped" || report.Healthy {
		t.Fatalf("dead child reported %q healthy=%v", report.State, report.Healthy)
	}
	if report.StoppedAt == nil || !strings.Contains(report.Detail, "exit status 7") {
		t.Fatalf("stopped report = %+v, want the real exit status", report)
	}
	if report.Probes != 0 || report.ProbeFailures != 0 {
		t.Fatalf("stopped child was probed: %+v", report)
	}
	if len(report.Events) != 2 || report.Events[1].Event != "exit" {
		t.Fatalf("log = %+v, want start + exit", report.Events)
	}
}

// The log is bounded: the newest lines stay, the oldest are dropped, and the
// counters keep counting.
func TestDiagnosticsLogIsBounded(t *testing.T) {
	server := fakeListener(t, http.StatusOK, "magpie", nil)
	defer server.Close()
	process := fakeProcess(t, server, "private-key")
	for range nativeLogLimit + 10 {
		process.record("event", "line")
	}
	report := decodeDiagnostics(t, json.RawMessage(mustMarshal(t, process.diagnostics(context.Background()))))
	if len(report.Events) != nativeLogLimit {
		t.Fatalf("log holds %d lines, want the bounded %d", len(report.Events), nativeLogLimit)
	}
	for index, event := range report.Events {
		want := "event"
		if index == len(report.Events)-1 {
			want = "probe_ok"
		}
		if event.Event != want {
			t.Fatalf("bounded log line %d = %+v, want %q", index, event, want)
		}
	}
	if report.Probes != 1 {
		t.Fatalf("probes after one retry = %d, want 1", report.Probes)
	}
}

// Without a managed child the report is explicit: no probe, no healthy state.
func TestDiagnosticsWithoutManagedProcessIsExplicit(t *testing.T) {
	var module *Module
	for name, raw := range map[string]json.RawMessage{
		"nil module":           module.Diagnostics(context.Background()),
		"module without child": (&Module{}).Diagnostics(context.Background()),
	} {
		report := decodeDiagnostics(t, raw)
		if report.State != "unavailable" || report.Healthy || report.Detail == "" {
			t.Fatalf("%s reported %+v, want an explicit unavailable state", name, report)
		}
		if report.Events == nil || len(report.Events) != 0 {
			t.Fatalf("%s events = %+v, want an empty log", name, report.Events)
		}
		if strings.Contains(string(raw), `"events":null`) {
			t.Fatalf("%s published a null log: %s", name, raw)
		}
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Un cierre registrado mientras la respuesta de la sonda sigue en vuelo gana a esa
// respuesta: nunca se publica "listo" ni una línea de éxito después de la salida.
func TestDiagnosticsKnownExitWinsOverAnswerInFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(started) })
		<-release
		_, _ = io.WriteString(w, "<title>magpie</title>")
	}))
	defer server.Close()
	command := exec.Command(sleepBinary(t), "30")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	// No competing Wait here: the real watcher below owns the child's exit.
	process := &nativeProcess{
		cmd: command, done: make(chan error, 1),
		webURL: server.URL, webHost: parsed.Host, webKey: "private-key",
		ports: nativePorts{Web: 4242}, client: server.Client(),
	}
	process.pid, process.startedAt = command.Process.Pid, time.Now().UTC()
	process.record("start", "managed process started")
	go process.watchExit()

	reports := make(chan nativeDiagnostics, 1)
	go func() { reports <- process.diagnostics(context.Background()) }()
	<-started
	if err := command.Process.Kill(); err != nil {
		close(release)
		t.Fatal(err)
	}
	<-process.done // el exit real ya está registrado antes de que el listener responda
	close(release)
	report := <-reports

	if report.State != "stopped" || report.Healthy {
		t.Fatalf("expected stopped/healthy=false after a known exit, observed state=%q healthy=%v (%+v)", report.State, report.Healthy, report)
	}
	if report.StoppedAt == nil || !strings.Contains(report.Detail, "killed") {
		t.Fatalf("expected stopped_at and the real exit detail, observed %+v", report)
	}
	for _, event := range report.Events {
		if event.Event == "probe_ok" {
			t.Fatalf("a success was published after the known exit: %+v", report.Events)
		}
	}
	if last := report.Events[len(report.Events)-1]; last.Event != "exit" {
		t.Fatalf("the log must end in the real exit, observed %+v", report.Events)
	}
}

// Un hijo que no atiende su propio cierre (detenido o colgado) no puede sobrevivir
// a la instalación: Close lo mata y lo cosecha en vez de devolver el contexto.
func TestCloseKillsChildThatDidNotStopOnItsOwn(t *testing.T) {
	command := exec.Command(sleepBinary(t), "30")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	process := &nativeProcess{
		cmd: command, done: make(chan error, 1),
		webURL: "http://127.0.0.1:1", webHost: "127.0.0.1:1", webKey: "private-key",
		ports: nativePorts{Web: 1}, client: &http.Client{Timeout: 200 * time.Millisecond},
	}
	go process.watchExit()
	if err := command.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := process.Close(ctx)
	if err == nil {
		t.Fatalf("Close reported success while the child had not stopped")
	}
	if alive := command.Process.Signal(syscall.Signal(0)); alive == nil {
		t.Fatalf("the child outlived Close (%v) and is now an orphan", err)
	}
}

// Un cierre pactado sigue siendo pacífico: el hijo que atiende su quit termina solo
// y Close no lo mata ni inventa un error.
func TestCloseLeavesAgreedShutdownUntouched(t *testing.T) {
	quit := filepath.Join(t.TempDir(), "quit")
	// A child that exits 0 as soon as its own quit call lands: the agreed shutdown
	// must stay peaceful and reap it without any signal.
	command := exec.Command("/bin/sh", "-c", `while [ ! -f "$1" ]; do sleep 0.05; done`, "sh", quit)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/window/quit" {
			if err := os.WriteFile(quit, nil, 0o600); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	process := &nativeProcess{
		cmd: command, done: make(chan error, 1),
		webURL: server.URL, webHost: parsed.Host, webKey: "private-key",
		ports: nativePorts{Web: 4242}, client: server.Client(),
	}
	go process.watchExit()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := process.Close(ctx); err != nil {
		t.Fatalf("agreed shutdown reported %v", err)
	}
	if alive := command.Process.Signal(syscall.Signal(0)); alive == nil {
		t.Fatal("the child should have exited on its own quit")
	}
}

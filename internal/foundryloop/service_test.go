package foundryloop

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	foundrybundle "workbench/third_party/foundry"
)

func requirePython(t *testing.T) {
	t.Helper()
	if _, err := findPython(context.Background(), ""); err != nil {
		t.Skip(err.Error())
	}
}

// newService runs the real embedded Foundry in a private state dir.
func newService(t *testing.T) *Service {
	t.Helper()
	requirePython(t)
	service, err := New(Options{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = service.Close(ctx)
	})
	return service
}

func get(t *testing.T, handler http.Handler, method, target, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	for name, value := range header {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestProxyServesFoundryUIAndAPI(t *testing.T) {
	service := newService(t)
	handler := service.Handler()

	page := get(t, handler, http.MethodGet, "/foundry/", "", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `src="./assets/`) {
		t.Fatalf("UI = %d %q, want the relative-asset index.html", page.Code, page.Body.String())
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") || strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP = %q, want framing allowed for the Workbench origin only", csp)
	}

	// The browser's own token, cookies and Origin never decide anything: the proxy supplies Foundry's.
	state := get(t, handler, http.MethodGet, "/foundry/api/state?compact=1", "", map[string]string{
		"X-Foundry-Token": "wrong", "Cookie": "workbench_session_x=1", "Origin": "https://evil.example", "Authorization": "Bearer nope",
	})
	if state.Code != http.StatusOK {
		t.Fatalf("state = %d %s", state.Code, state.Body.String())
	}
	var decoded struct {
		Sources []any          `json:"sources"`
		Config  map[string]any `json:"config"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &decoded); err != nil || decoded.Config == nil || decoded.Sources == nil {
		t.Fatalf("state body is not Foundry's contract: %v %s", err, state.Body.String())
	}
	if len(decoded.Sources) != 0 {
		t.Fatalf("a fresh Foundry registered %d sources on its own; capture must stay opt-in", len(decoded.Sources))
	}

	action := get(t, handler, http.MethodPost, "/foundry/api/action", `{"action":"nope"}`, map[string]string{"Content-Type": "application/json"})
	if action.Code == http.StatusUnauthorized || action.Code == http.StatusForbidden {
		t.Fatalf("POST was refused (%d %s): the proxy did not supply the token/Host", action.Code, action.Body.String())
	}

	if redirect := get(t, handler, http.MethodGet, "/foundry?view=learning", "", nil); redirect.Code != http.StatusPermanentRedirect || redirect.Header().Get("Location") != "/foundry/?view=learning" {
		t.Fatalf("/foundry = %d %q, want a redirect to the slash form", redirect.Code, redirect.Header().Get("Location"))
	}
	if escape := get(t, handler, http.MethodGet, "/foundry/../etc/passwd", "", nil); escape.Code != http.StatusBadRequest {
		t.Fatalf("dot segment = %d, want 400", escape.Code)
	}
	if missing := get(t, handler, http.MethodGet, "/foundry/foundry_loop/store.py", "", nil); missing.Code != http.StatusNotFound {
		t.Fatalf("package source = %d, want 404: only the UI entrypoints are served", missing.Code)
	}
}

// The package reads some files next to itself (the guided example's project); a runtime that
// ships only the package would pass every other test and fail the first "Preparar ejemplo".
func TestEmbeddedRuntimeCarriesWhatFoundryReadsAtRuntime(t *testing.T) {
	tree := foundrybundle.Runtime()
	for _, name := range []string{"foundry_loop/__main__.py", "foundry_loop/web/index.html", "examples/http-service"} {
		if _, err := fs.Stat(tree, name); err != nil {
			t.Fatalf("embedded runtime lacks %s: %v", name, err)
		}
	}
}

func TestGuidedExampleRunsFromTheEmbeddedRuntime(t *testing.T) {
	service := newService(t)
	handler := service.Handler()
	started := get(t, handler, http.MethodPost, "/foundry/api/action", `{"action":"example"}`, map[string]string{"Content-Type": "application/json"})
	if started.Code != http.StatusOK {
		t.Fatalf("example = %d %s", started.Code, started.Body.String())
	}
	var job struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &job); err != nil || job.JobID == "" {
		t.Fatalf("example receipt = %s", started.Body.String())
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		state := get(t, handler, http.MethodGet, "/foundry/api/state?compact=1", "", nil)
		var body struct {
			Jobs     []struct{ ID, Kind, Status, Error string } `json:"jobs"`
			Sessions []any                                      `json:"sessions"`
			Cases    []any                                      `json:"cases"`
		}
		if err := json.Unmarshal(state.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, candidate := range body.Jobs {
			if candidate.ID != job.JobID {
				continue
			}
			if candidate.Status == "failed" {
				t.Fatalf("example job failed: %s", candidate.Error)
			}
			if candidate.Status == "succeeded" {
				if len(body.Sessions) != 1 || len(body.Cases) == 0 {
					t.Fatalf("example left %d sessions and %d cases", len(body.Sessions), len(body.Cases))
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("example job did not finish: %s", state.Body.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestCloseStopsFoundry(t *testing.T) {
	service := newService(t)
	if response := get(t, service.Handler(), http.MethodGet, "/foundry/health", "", nil); response.Code != http.StatusOK {
		t.Fatalf("health = %d %s", response.Code, response.Body.String())
	}
	port := service.port
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := probe(port); ok {
		t.Fatal("Foundry still answers after Close")
	}
	if response := get(t, service.Handler(), http.MethodGet, "/foundry/health", "", nil); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("after Close = %d, want 503", response.Code)
	}
}

func TestRestartsAfterFoundryStops(t *testing.T) {
	service := newService(t)
	handler := service.Handler()
	if response := get(t, handler, http.MethodGet, "/foundry/health", "", nil); response.Code != http.StatusOK {
		t.Fatalf("health = %d", response.Code)
	}
	done, first := service.done, service.port
	// The UI can stop the service (its shutdown action); the next request brings it back.
	get(t, handler, http.MethodPost, "/foundry/api/action", `{"action":"shutdown"}`, map[string]string{"Content-Type": "application/json"})
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Foundry did not exit after the shutdown action")
	}
	response := get(t, handler, http.MethodGet, "/foundry/health", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("after shutdown = %d %s, want a restarted Foundry", response.Code, response.Body.String())
	}
	if service.port == first && service.done == done {
		t.Fatal("no new process was started")
	}
}

func TestAdoptsFoundryFromPreviousRun(t *testing.T) {
	requirePython(t)
	state := t.TempDir()
	first, err := New(Options{StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close(context.Background()) })
	if response := get(t, first.Handler(), http.MethodGet, "/foundry/health", "", nil); response.Code != http.StatusOK {
		t.Fatalf("first health = %d", response.Code)
	}
	second, err := New(Options{StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	if response := get(t, second.Handler(), http.MethodGet, "/foundry/api/state?compact=1", "", nil); response.Code != http.StatusOK {
		t.Fatalf("adopted state = %d %s", response.Code, response.Body.String())
	}
	if !second.adopted || second.port != first.port {
		t.Fatalf("second service adopted=%v port=%d, want the running Foundry on %d", second.adopted, second.port, first.port)
	}
	if err := second.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := probe(first.port); !ok {
		t.Fatal("closing an adopting service stopped a Foundry it does not own")
	}
}

func TestUnavailableWhenPythonIsMissing(t *testing.T) {
	service, err := New(Options{StateDir: t.TempDir(), Python: "definitely-not-python-3"})
	if err != nil {
		t.Fatal(err)
	}
	response := get(t, service.Handler(), http.MethodGet, "/foundry/", "", nil)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") == "" {
		t.Fatalf("missing python = %d %v", response.Code, response.Header())
	}
	var body struct{ Error, Detail string }
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error != "foundry_unavailable" || !strings.Contains(body.Detail, "Python 3.11") {
		t.Fatalf("body = %s, want an actionable detail", response.Body.String())
	}
	// A broken install is not retried on every request.
	service.mu.Lock()
	service.lastTry = time.Now()
	service.mu.Unlock()
	if again := get(t, service.Handler(), http.MethodGet, "/foundry/", "", nil); again.Code != http.StatusServiceUnavailable {
		t.Fatalf("second request = %d", again.Code)
	}
}

// fakePython writes an executable that prints the version a real interpreter would.
func fakePython(t *testing.T, dir, name string, version int) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\necho %d\n", version)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestFindPythonSkipsOldInterpretersAndExplainsWhatItSaw(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	ctx := context.Background()

	if _, err := findPython(ctx, ""); err == nil || !strings.Contains(err.Error(), "found none") {
		t.Fatalf("empty PATH: %v, want 'found none'", err)
	}

	fakePython(t, dir, "python3", 310)
	_, err := findPython(ctx, "")
	if err == nil || !strings.Contains(err.Error(), "python3 is 3.10") || !strings.Contains(err.Error(), "WORKBENCH_FOUNDRY_PYTHON") {
		t.Fatalf("old python3: %v, want its version and what to do", err)
	}

	// An old python3 does not hide a newer one installed beside it.
	fakePython(t, dir, "python3.12", 312)
	got, err := findPython(ctx, "")
	if err != nil || filepath.Base(got) != "python3.12" {
		t.Fatalf("with python3.12 beside python3.10: %q %v", got, err)
	}

	// A configured interpreter is the only one used, even when another would do.
	if _, err := findPython(ctx, "python3"); err == nil {
		t.Fatal("explicit python3 (3.10) was replaced by another interpreter")
	}
	fakePython(t, dir, "mine", 313)
	if got, err := findPython(ctx, "mine"); err != nil || filepath.Base(got) != "mine" {
		t.Fatalf("explicit interpreter: %q %v", got, err)
	}
}

func TestChildEnvDropsWorkbenchConfiguration(t *testing.T) {
	env := childEnv([]string{"HOME=/h", "WORKBENCH_TOKEN=secret", "WORKBENCH_STATE=/s", "PYTHONPATH=/old", "FOUNDRY_DATA=/x", "PATH=/bin"}, "/rt")
	joined := strings.Join(env, "\n")
	for _, leaked := range []string{"WORKBENCH_", "secret", "/old", "FOUNDRY_DATA"} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("child environment keeps %q: %v", leaked, env)
		}
	}
	for _, want := range []string{"HOME=/h", "PATH=/bin", "PYTHONPATH=/rt"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("child environment lost %q: %v", want, env)
		}
	}
}

func TestExtractRuntimeIsAtomicAndSkipsBytecode(t *testing.T) {
	tree := fstest.MapFS{
		"foundry_loop/__init__.py":                   {Data: []byte("x = 1\n")},
		"foundry_loop/web/index.html":                {Data: []byte("<html>")},
		"foundry_loop/__pycache__/store.cpython.pyc": {Data: []byte("bytecode")},
	}
	root := t.TempDir()
	first, err := extractRuntime(tree, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(first, "foundry_loop", "__pycache__")); !os.IsNotExist(err) {
		t.Fatalf("bytecode was extracted: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(first, "foundry_loop", "__init__.py"))
	if err != nil || string(data) != "x = 1\n" {
		t.Fatalf("__init__.py = %q %v", data, err)
	}
	again, err := extractRuntime(tree, root)
	if err != nil || again != first {
		t.Fatalf("second extraction = %q %v, want reuse of %q", again, err, first)
	}

	// A different build gets its own directory; an interrupted extraction (no marker) is redone.
	tree["foundry_loop/__init__.py"] = &fstest.MapFile{Data: []byte("x = 2\n")}
	changed, err := extractRuntime(tree, root)
	if err != nil || changed == first {
		t.Fatalf("changed content = %q %v, want a new directory", changed, err)
	}
	if err := os.Remove(filepath.Join(changed, ".complete")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(changed, "foundry_loop", "__init__.py"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	redone, err := extractRuntime(tree, root)
	if err != nil || redone != changed {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(redone, "foundry_loop", "__init__.py"))
	if string(data) != "x = 2\n" {
		t.Fatalf("redone extraction kept %q", data)
	}
}

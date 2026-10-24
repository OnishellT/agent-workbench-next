package models

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"workbench/internal/config"
)

const (
	nativeReadyTimeout = 12 * time.Second
	nativeJSONLimit    = 4 << 20
	nativeProbeTimeout = 5 * time.Second
	nativeKillTimeout  = 5 * time.Second
	nativeLogLimit     = 64
	nativeDetailLimit  = 160
)

// nativeEvent is one bounded lifecycle line; Detail never carries the private web
// key, the child environment or raw process output.
type nativeEvent struct {
	Time   time.Time `json:"time"`
	Event  string    `json:"event"`
	Detail string    `json:"detail,omitempty"`
}

// nativeDiagnostics is the read-only report: mounted is not readiness, so it always
// re-probes the private listener first.
type nativeDiagnostics struct {
	ID            string        `json:"id"`
	State         string        `json:"state"`
	Healthy       bool          `json:"healthy"`
	Detail        string        `json:"detail,omitempty"`
	PID           int           `json:"pid,omitempty"`
	StartedAt     *time.Time    `json:"started_at,omitempty"`
	ReadyAt       *time.Time    `json:"ready_at,omitempty"`
	StoppedAt     *time.Time    `json:"stopped_at,omitempty"`
	Probes        int           `json:"probes"`
	ProbeFailures int           `json:"probe_failures"`
	Events        []nativeEvent `json:"events"`
}

func unavailableDiagnostics(detail string) nativeDiagnostics {
	return nativeDiagnostics{ID: "models", State: "unavailable", Detail: detail, Events: []nativeEvent{}}
}

type nativePorts struct {
	Site    int `json:"site"`
	Web     int `json:"web"`
	Gateway int `json:"gateway"`
}

type nativeProcess struct {
	cmd        *exec.Cmd
	done       chan error
	webURL     string
	gateway    string
	siteOrigin string
	webHost    string
	webKey     string
	ports      nativePorts
	client     *http.Client
	stopOnce   sync.Once
	group      bool // the child leads its own process group

	callerMu    sync.Mutex
	callerCache map[[sha256.Size]byte]time.Time // validated public keys (hashed) -> expiry

	mu         sync.Mutex
	pid        int
	startedAt  time.Time
	readyAt    time.Time
	stoppedAt  time.Time
	exitDetail string
	exited     bool
	probes     int
	failures   int
	events     []nativeEvent
}

// newNativeHTTPClient talks to the loopback child: no proxy, no redirects.
func newNativeHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 15 * time.Second}, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// webCookie is the private web-key cookie the child's web listener requires.
func (p *nativeProcess) webCookie() string {
	return "magpie_web_" + strconv.Itoa(p.ports.Web) + "=" + p.webKey
}

func startNativeProcess(stateDir, home, publicURL string) (*nativeProcess, error) {
	if err := privateDir(stateDir); err != nil {
		return nil, fmt.Errorf("prepare native Magpie state: %w", err)
	}
	ports, err := loadNativePorts(stateDir)
	if err != nil {
		return nil, err
	}
	if path := strings.TrimSpace(os.Getenv("WORKBENCH_MAGPIE_ATTACH_FILE")); path != "" {
		return attachNativeProcess(path, publicURL, ports.Site)
	}
	binary, err := resolveMagpieBinary()
	if err != nil {
		return nil, err
	}
	key, err := loadNativeWebKey(filepath.Join(stateDir, "native-web.key"))
	if err != nil {
		return nil, err
	}
	siteOrigin, err := siteOrigin(publicURL, ports.Site)
	if err != nil {
		return nil, err
	}
	webHost := net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Web))
	webURL := "http://" + webHost
	gateway := net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Gateway))
	process := &nativeProcess{
		webURL: webURL, gateway: "http://" + gateway, siteOrigin: siteOrigin,
		webHost: webHost, webKey: key, ports: ports,
		client: newNativeHTTPClient(),
	}
	if err := prepareNativeHome(home, stateDir); err != nil {
		return nil, err
	}
	binaryEnv := nativeEnvironment(home, stateDir, siteOrigin, gateway, key)
	process.cmd = exec.Command(binary, "serve", "--web=127.0.0.1:"+strconv.Itoa(ports.Web))
	process.cmd.Env = binaryEnv
	// Own process group so Close can signal Magpie and everything it spawned; the
	// kernel kills the child if Workbench dies without running Close.
	process.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	process.group = true
	// Native Magpie prints its sign-in URL (which contains the private web key).
	process.cmd.Stdout, process.cmd.Stderr = io.Discard, io.Discard
	if err := process.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start native Magpie: %w", err)
	}
	process.pid = process.cmd.Process.Pid
	process.startedAt = time.Now().UTC()
	process.record("start", "managed process started")
	process.done = make(chan error, 1)
	go process.watchExit()
	if err := process.waitReady(); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = process.Close(ctx)
		return nil, err
	}
	return process, nil
}

func resolveMagpieBinary() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("WORKBENCH_MAGPIE_BIN")); configured != "" {
		return executableFile(configured)
	}
	workbench, err := os.Executable()
	if err != nil {
		return "", errors.New("native Magpie binary is required (set WORKBENCH_MAGPIE_BIN)")
	}
	candidate := filepath.Join(filepath.Dir(workbench), "magpie")
	if binary, err := executableFile(candidate); err == nil {
		return binary, nil
	}
	return "", errors.New("native Magpie binary is required beside Workbench or via WORKBENCH_MAGPIE_BIN")
}

func executableFile(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("WORKBENCH_MAGPIE_BIN must be an absolute executable path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("native Magpie binary is missing or not executable")
	}
	return filepath.Clean(path), nil
}

func nativeEnvironment(home, stateRoot, publicURL, gateway, webKey string) []string {
	xdg := nativeXDGs(home)
	values := map[string]string{
		"HOME":              home,
		"XDG_CONFIG_HOME":   xdg["XDG_CONFIG_HOME"],
		"XDG_CACHE_HOME":    xdg["XDG_CACHE_HOME"],
		"XDG_DATA_HOME":     xdg["XDG_DATA_HOME"],
		"XDG_STATE_HOME":    xdg["XDG_STATE_HOME"],
		"XDG_RUNTIME_DIR":   filepath.Join(stateRoot, "run"),
		"PATH":              os.Getenv("PATH"),
		"MAGPIE_BUN":        os.Getenv("MAGPIE_BUN"),
		"MAGPIE_ADDR":       gateway,
		"MAGPIE_PUBLIC_URL": "http://" + gateway,
		"MAGPIE_WEB_URL":    publicURL,
		"MAGPIE_WEB_KEY":    webKey,
	}
	out := make([]string, 0, len(values))
	for name, value := range values {
		out = append(out, name+"="+value)
	}
	return out
}

// nativeXDGs resolves the profile locations the actor must see. When home is the
// process operator's own HOME, pre-existing absolute XDG overrides are the user's
// legitimate locations (the profile they open normally) and are preserved. For
// any other home (another account, an ensayo profile) the operator environment
// must not leak into the actor, so home-relative defaults apply. Relative or
// empty overrides are never honored. Only these four profile locations are ever
// inherited; the runtime dir stays under Workbench's private state root and no
// other operator variable reaches the child.
func nativeXDGs(home string) map[string]string {
	defaults := map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
	}
	if filepath.Clean(os.Getenv("HOME")) != home {
		return defaults
	}
	for name := range defaults {
		if value := os.Getenv(name); value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value {
			defaults[name] = value
		}
	}
	return defaults
}

// prepareNativeHome readies the native actor's real user HOME without taking
// ownership of it. The HOME directory itself is never created, chmodded, or
// traversed through a symlink: Magpie must see the user's own agent and profile
// files. Only missing XDG subdirectories are created; pre-existing user
// directories keep their own permissions. The process run directory stays under
// Workbench's private state root, never beside the real HOME.
func prepareNativeHome(home, stateRoot string) error {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return errors.New("native Magpie requires the absolute native user home")
	}
	if info, err := os.Lstat(home); err != nil {
		return fmt.Errorf("prepare native Magpie home: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("native Magpie home is not a real directory")
	}
	// Ensure the effective profile locations (the user's own XDG overrides when
	// home is the operator's HOME, else home-relative defaults). Existing user
	// directories keep their permissions; nothing is ever copied.
	for _, dir := range nativeXDGs(home) {
		if err := ensureNativeSubdir(dir); err != nil {
			return fmt.Errorf("prepare native Magpie home: %w", err)
		}
	}
	if err := privateDir(filepath.Join(stateRoot, "run")); err != nil {
		return fmt.Errorf("prepare native Magpie home: %w", err)
	}
	return nil
}

// ensureNativeSubdir creates a missing Magpie data directory under the real user
// HOME. Existing user directories are left untouched: no chmod, no ownership change.
func ensureNativeSubdir(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("native Magpie directory is not a real directory: %s", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.MkdirAll(path, 0o700)
}

func privateDir(path string) error {
	if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
		return fmt.Errorf("private directory is not a real directory: %s", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func loadNativePorts(stateDir string) (nativePorts, error) {
	path := filepath.Join(stateDir, "native-ports.json")
	ports, err := readNativePorts(path)
	if errors.Is(err, os.ErrNotExist) {
		ports, err = allocateNativePorts()
		if err != nil {
			return ports, err
		}
		data, err := json.Marshal(ports)
		if err != nil {
			return ports, err
		}
		// Another starter may have won the create; the file on disk is the truth.
		if err := writePrivateFile(path, data); errors.Is(err, os.ErrExist) {
			ports, err = readNativePorts(path)
			if err != nil {
				return ports, err
			}
		} else if err != nil {
			return ports, err
		}
	} else if err != nil {
		return ports, err
	}
	if ports.Site < 1 || ports.Site > 65535 || ports.Web < 1 || ports.Web > 65535 || ports.Gateway < 1 || ports.Gateway > 65535 || ports.Site == ports.Web || ports.Site == ports.Gateway || ports.Web == ports.Gateway {
		return ports, errors.New("native Magpie ports must be distinct numeric ports")
	}
	return ports, nil
}

// readNativePorts returns os.ErrNotExist (wrapped) when no state file exists yet.
func readNativePorts(path string) (nativePorts, error) {
	var ports nativePorts
	data, err := readPrivateFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ports, err
		}
		return ports, errors.New("native Magpie port state is not a private regular file")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ports); err != nil {
		return ports, errors.New("native Magpie port state is invalid")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ports, errors.New("native Magpie port state has trailing data")
	}
	return ports, nil
}

// readPrivateFile checks the entry with Lstat before reading: it must be a regular
// file reachable by its owner only. A missing file reports os.ErrNotExist.
func readPrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("not a private regular file")
	}
	return os.ReadFile(path)
}

func allocateNativePorts() (nativePorts, error) {
	listeners := make([]net.Listener, 0, 3)
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	ports := make([]int, 0, 3)
	for range 3 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nativePorts{}, fmt.Errorf("allocate private native Magpie port: %w", err)
		}
		listeners = append(listeners, listener)
		_, value, err := net.SplitHostPort(listener.Addr().String())
		if err != nil {
			return nativePorts{}, err
		}
		port, err := strconv.Atoi(value)
		if err != nil {
			return nativePorts{}, err
		}
		ports = append(ports, port)
	}
	return nativePorts{Site: ports[0], Web: ports[1], Gateway: ports[2]}, nil
}

func loadNativeWebKey(path string) (string, error) {
	data, err := readPrivateFile(path)
	if errors.Is(err, os.ErrNotExist) {
		value := make([]byte, 32)
		if _, err := rand.Read(value); err != nil {
			return "", err
		}
		key := base64.RawURLEncoding.EncodeToString(value)
		clear(value)
		writeErr := writePrivateFile(path, []byte(key))
		if writeErr == nil {
			return key, nil
		}
		if !errors.Is(writeErr, os.ErrExist) {
			return "", writeErr
		}
		// Lost the create race: use the key that is on disk, not ours.
		data, err = readPrivateFile(path)
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "", errors.New("native Magpie web key is not a private regular file")
	}
	key := string(data)
	if len(key) < 32 || strings.Trim(key, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~") != "" {
		return "", errors.New("native Magpie web key is invalid")
	}
	return key, nil
}

// writePrivateFile publishes data at path atomically and exclusively: the content is
// written to a private temporary file and hard-linked into place, so a concurrent
// reader never sees a partial file and a lost race reports os.ErrExist.
func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Link(tmp, path)
}

func siteOrigin(publicURL string, port int) (string, error) {
	if configured := strings.TrimSpace(os.Getenv("WORKBENCH_MAGPIE_ORIGIN")); configured != "" {
		origin, err := config.NormalizePublicURL(configured)
		if err != nil {
			return "", fmt.Errorf("WORKBENCH_MAGPIE_ORIGIN: %w", err)
		}
		root, err := config.NormalizePublicURL(publicURL)
		if err != nil || strings.EqualFold(origin, root) {
			return "", errors.New("native Magpie requires a public origin distinct from Workbench")
		}
		return origin, nil
	}
	u, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return "", errors.New("native Magpie requires Workbench's canonical public origin")
	}
	if u.Scheme == "https" {
		return "", errors.New("set WORKBENCH_MAGPIE_ORIGIN to a distinct HTTPS origin routed to the native Magpie site")
	}
	host := u.Hostname()
	address, parseErr := netip.ParseAddr(host)
	if parseErr != nil || !address.IsLoopback() {
		return "", errors.New("native Magpie local origin must use a loopback address")
	}
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), nil
}

// record appends one bounded lifecycle line; the ring keeps the newest.
func (p *nativeProcess) record(event, detail string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.appendLocked(event, detail)
}

// appendLocked appends one bounded line; the caller holds the lock.
func (p *nativeProcess) appendLocked(event, detail string) {
	if len(detail) > nativeDetailLimit {
		detail = detail[:nativeDetailLimit]
	}
	p.events = append(p.events, nativeEvent{Time: time.Now().UTC(), Event: event, Detail: detail})
	if len(p.events) > nativeLogLimit {
		// Shift in place: no allocation per event once the ring is full.
		p.events = p.events[:copy(p.events, p.events[len(p.events)-nativeLogLimit:])]
	}
}

func (p *nativeProcess) watchExit() {
	err := p.cmd.Wait()
	detail := "process exited"
	if err != nil {
		detail = err.Error()
	}
	if len(detail) > nativeDetailLimit {
		detail = detail[:nativeDetailLimit]
	}
	p.mu.Lock()
	p.exited, p.stoppedAt, p.exitDetail = true, time.Now().UTC(), detail
	p.mu.Unlock()
	p.record("exit", detail)
	p.done <- err
	close(p.done)
}

// checkReady is the real readiness read the startup gate uses; its errors carry no
// key, listener address or response body.
func (p *nativeProcess) checkReady(ctx context.Context) error {
	if p == nil || p.client == nil || p.webURL == "" {
		return errors.New("native Magpie is not managed by this instance")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.webURL+"/", nil)
	if err != nil {
		return errors.New("native Magpie readiness request is invalid")
	}
	request.Host = p.webHost
	request.Header.Set("Cookie", p.webCookie())
	request.Header.Set("Origin", p.webURL)
	response, err := p.client.Do(request)
	if err != nil {
		return errors.New("native Magpie readiness listener is unreachable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return errors.New("native Magpie readiness response is unreadable")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("native Magpie readiness listener answered HTTP %d", response.StatusCode)
	}
	if !strings.Contains(string(body), "magpie") {
		return errors.New("native Magpie readiness response did not identify Magpie")
	}
	return nil
}

// diagnostics re-probes readiness and copies the bounded log; read-only.
func (p *nativeProcess) diagnostics(ctx context.Context) nativeDiagnostics {
	if p == nil || (p.webURL == "" && (p.cmd == nil || p.cmd.Process == nil)) {
		return unavailableDiagnostics("no native Magpie connection in this instance")
	}
	p.mu.Lock()
	exited := p.exited
	p.mu.Unlock()
	var probeErr error
	if !exited {
		probeCtx, cancel := context.WithTimeout(ctx, nativeProbeTimeout)
		probeErr = p.checkReady(probeCtx)
		cancel()
	}
	// The exit wins over a probe in flight: one coherent snapshot, and the outcome is
	// logged under the same lock so it cannot land after the exit.
	p.mu.Lock()
	if !exited {
		p.probes++
		switch {
		case p.exited:
			// The exit won the race: the stale answer is not a success and is not logged.
		case probeErr == nil:
			p.readyAt = time.Now().UTC()
			p.appendLocked("probe_ok", "readiness read answered")
		default:
			p.failures++
			p.appendLocked("probe_failed", probeErr.Error())
		}
	}
	report := nativeDiagnostics{
		ID: "models", PID: p.pid,
		StartedAt: timePointer(p.startedAt), ReadyAt: timePointer(p.readyAt), StoppedAt: timePointer(p.stoppedAt),
		Probes: p.probes, ProbeFailures: p.failures,
		Events: append([]nativeEvent{}, p.events...),
	}
	exited, exitDetail := p.exited, p.exitDetail
	p.mu.Unlock()
	switch {
	case exited:
		report.State, report.Detail = "stopped", exitDetail
	case probeErr != nil:
		report.State, report.Detail = "unready", probeErr.Error()
	default:
		report.State, report.Healthy = "ready", true
	}
	return report
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func (p *nativeProcess) waitReady() error {
	deadline := time.NewTimer(nativeReadyTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-p.done:
			if err == nil {
				err = errors.New("process exited")
			}
			return fmt.Errorf("native Magpie exited before its private web listener was ready: %v", err)
		case <-deadline.C:
			err := errors.New("native Magpie private web listener did not become ready")
			p.record("unready", err.Error())
			return err
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			err := p.checkReady(ctx)
			cancel()
			if err == nil {
				p.record("ready", "private web listener answered the readiness read")
				return nil
			}
		}
	}
}

func (p *nativeProcess) siteHost() string {
	return p.webHost
}

func (p *nativeProcess) NativeJSON(ctx context.Context, path string) (json.RawMessage, error) {
	return p.nativeJSON(ctx, http.MethodGet, path, nil)
}

func (p *nativeProcess) nativeJSON(ctx context.Context, method, path string, body []byte) (json.RawMessage, error) {
	if p == nil || p.webURL == "" {
		return nil, errors.New("native Magpie is unavailable")
	}
	u, err := url.ParseRequestURI(path)
	if err != nil || !strings.HasPrefix(u.Path, "/api/") || u.Host != "" || u.Fragment != "" || u.User != nil {
		return nil, errors.New("native Magpie API path is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, method, p.webURL+u.RequestURI(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Host = p.webHost
	request.Header.Set("Cookie", p.webCookie())
	request.Header.Set("Origin", p.webURL)
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := p.client.Do(request)
	if err != nil {
		return nil, errors.New("native Magpie API is unavailable")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, nativeJSONLimit+1))
	if err != nil || len(data) > nativeJSONLimit {
		return nil, errors.New("native Magpie API response is invalid or too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("native Magpie API returned HTTP %d", response.StatusCode)
	}
	if method == http.MethodPost && response.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if !json.Valid(data) {
		return nil, errors.New("native Magpie API returned invalid JSON")
	}
	return json.RawMessage(data), nil
}

type nativeCallerKeys struct {
	Keys []struct {
		ID       string `json:"id"`
		Masked   string `json:"masked"`
		Off      bool   `json:"off"`
		Internal bool   `json:"internal"`
	} `json:"keys"`
}

const (
	callerCacheTTL  = 30 * time.Second
	callerCacheSize = 128
)

// callerAccepted reports whether this key validated recently, and remembers a fresh
// validation. Only a SHA-256 of the key is kept, for a short time and in bounded
// number, so a revoked key stops working within callerCacheTTL.
func (p *nativeProcess) callerAccepted(candidate string, remember bool) bool {
	digest := sha256.Sum256([]byte(candidate))
	now := time.Now()
	p.callerMu.Lock()
	defer p.callerMu.Unlock()
	if !remember {
		expiry, ok := p.callerCache[digest]
		return ok && now.Before(expiry)
	}
	if p.callerCache == nil {
		p.callerCache = make(map[[sha256.Size]byte]time.Time)
	}
	if len(p.callerCache) >= callerCacheSize {
		for key, expiry := range p.callerCache {
			if !now.Before(expiry) {
				delete(p.callerCache, key)
			}
		}
		if len(p.callerCache) >= callerCacheSize {
			clear(p.callerCache)
		}
	}
	p.callerCache[digest] = now.Add(callerCacheTTL)
	return true
}

func (p *nativeProcess) validPublicCaller(ctx context.Context, candidate string) (bool, error) {
	if len(candidate) < 8 || len(candidate) > 256 {
		return false, nil
	}
	if p.callerAccepted(candidate, false) {
		return true, nil
	}
	data, err := p.NativeJSON(ctx, "/api/caller-keys")
	if err != nil {
		return false, err
	}
	var keys nativeCallerKeys
	if json.Unmarshal(data, &keys) != nil {
		return false, errors.New("native Magpie caller keys are invalid")
	}
	for _, key := range keys.Keys {
		if key.ID == "" || key.Off || key.Internal || key.Masked == "" {
			continue
		}
		if _, suffix, ok := strings.Cut(key.Masked, "…"); !ok || len(suffix) == 0 || !strings.HasSuffix(candidate, suffix) {
			continue
		}
		body, _ := json.Marshal(map[string]string{"key": key.ID})
		copyJSON, err := p.nativeJSON(ctx, http.MethodPost, "/api/caller-keys/copy-key", body)
		if err != nil {
			return false, err
		}
		var copied struct {
			Secret string `json:"secret"`
		}
		if json.Unmarshal(copyJSON, &copied) != nil || copied.Secret == "" {
			return false, errors.New("native Magpie caller key validation failed")
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(copied.Secret)) == 1 {
			return p.callerAccepted(candidate, true), nil
		}
	}
	return false, nil
}

func bearerCredential(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	fields := strings.Fields(values[0])
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || len(fields[1]) < 8 || len(fields[1]) > 256 {
		return "", false
	}
	return fields[1], true
}

// stripProxyHeaders removes identity, origin and forwarding headers before a request
// is replayed to the loopback child. The browser-facing site also drops the caller's
// Authorization and Referer; the gateway keeps Authorization for the child.
func stripProxyHeaders(h http.Header, site bool) {
	for name := range h {
		switch lower := strings.ToLower(name); {
		case lower == "host", lower == "origin", lower == "cookie", lower == "proxy-authorization", lower == "forwarded",
			strings.HasPrefix(lower, "x-forwarded-"), lower == "x-real-ip", lower == "cf-connecting-ip", lower == "true-client-ip":
			h.Del(name)
		case site && (lower == "authorization" || lower == "referer"):
			h.Del(name)
		}
	}
}

func (p *nativeProcess) SiteHandler() http.Handler {
	target, _ := url.Parse(p.webURL)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			out := req.Out
			out.URL.Scheme, out.URL.Host = target.Scheme, target.Host
			out.Host = p.siteHost()
			stripProxyHeaders(out.Header, true)
			out.Header.Set("Cookie", p.webCookie())
			if req.In.Header.Get("Origin") != "" {
				out.Header.Set("Origin", p.webURL)
			}
		},
		ModifyResponse: func(response *http.Response) error {
			cookies := response.Header.Values("Set-Cookie")
			response.Header.Del("Set-Cookie")
			for _, cookie := range cookies {
				if !strings.HasPrefix(strings.ToLower(cookie), "magpie_web_") && !strings.HasPrefix(strings.ToLower(cookie), "workbench_magpie_") {
					response.Header.Add("Set-Cookie", cookie)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "native Magpie is unavailable", http.StatusServiceUnavailable)
		},
		FlushInterval: -1,
	}
	return proxy
}

func (p *nativeProcess) GatewayHandler() http.Handler {
	proxy := p.gatewayProxy(true)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		credential, ok := bearerCredential(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "valid native Magpie API key required", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		valid, err := p.validPublicCaller(ctx, credential)
		cancel()
		if err != nil {
			http.Error(w, "native Magpie authorization unavailable", http.StatusServiceUnavailable)
			return
		}
		if !valid {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "invalid native Magpie API key", http.StatusUnauthorized)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

// AgentGatewayHandler serves this computer's own agents; the public entry marks its
// callers as another machine's so the child applies sharing and gateway keys.
func (p *nativeProcess) AgentGatewayHandler() http.Handler { return p.gatewayProxy(false) }

func (p *nativeProcess) gatewayProxy(remote bool) http.Handler {
	target, _ := url.Parse(p.gateway)
	return &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			out := req.Out
			out.URL.Scheme, out.URL.Host = target.Scheme, target.Host
			out.Host = target.Host
			stripProxyHeaders(out.Header, false)
			// The child is reached over loopback, so it would take the public caller for
			// this computer's. The mark says another machine's, as the legacy root's
			// proxy did: only explicit sharing and an enabled gateway key answer it.
			if remote {
				out.Header.Set("X-Forwarded-For", "remote-workbench-client")
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "native Magpie gateway is unavailable", http.StatusServiceUnavailable)
		},
		FlushInterval: -1,
	}
}

func (p *nativeProcess) Close(ctx context.Context) error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	p.stopOnce.Do(func() {
		if _, err := p.nativeJSON(ctx, http.MethodPost, "/api/window/quit", []byte("{}")); err != nil {
			_ = p.signal(syscall.SIGTERM)
		}
	})
	select {
	case err, ok := <-p.done:
		if !ok || err == nil {
			return nil
		}
		return fmt.Errorf("native Magpie stopped with error: %w", err)
	case <-ctx.Done():
		// A child that did not answer its own quit must not outlive this installation as
		// an orphan holding the private listeners; the forced stop is bounded and reaps it.
		return errors.Join(ctx.Err(), p.forceStop())
	}
}

// signal delivers sig to the child's whole process group when it leads one.
func (p *nativeProcess) signal(sig syscall.Signal) error {
	if p.group && p.cmd.Process.Pid > 0 {
		return syscall.Kill(-p.cmd.Process.Pid, sig)
	}
	return p.cmd.Process.Signal(sig)
}

// forceStop kills a child that ignored its quit (a stopped or wedged process ignores
// signals that need it to run) and reaps it, so Close never leaves an orphan.
func (p *nativeProcess) forceStop() error {
	if err := p.signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop native Magpie child: %w", err)
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(nativeKillTimeout):
		return errors.New("native Magpie child did not exit after being killed")
	}
}

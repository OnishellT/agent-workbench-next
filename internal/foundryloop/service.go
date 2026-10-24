// Package foundryloop runs the vendored Conversation Foundry (Python, loopback only) as
// a managed child of Workbench and serves it, behind the Workbench session, under
// /foundry/. Foundry keeps its own store, token and Host/Origin checks; this package
// never stores its token anywhere but memory and never exposes it to the browser.
package foundryloop

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	foundrybundle "workbench/third_party/foundry"
)

const (
	// Prefix is the single-origin path Foundry's UI and API are served under.
	Prefix = "/foundry"

	application   = "conversation-foundry"
	minPython     = 311
	startTimeout  = 25 * time.Second
	retryAfter    = 5 * time.Second
	stopTimeout   = 8 * time.Second
	logTailBytes  = 2048
	healthTimeout = 1500 * time.Millisecond
)

// Options configures a Service. Zero values pick the production defaults.
type Options struct {
	// StateDir is the Workbench state directory; Foundry's data lives in StateDir/foundry-loop/data.
	StateDir string
	// DataDir overrides the Foundry data directory (WORKBENCH_FOUNDRY_DATA).
	DataDir string
	// Python overrides the interpreter (WORKBENCH_FOUNDRY_PYTHON); Foundry needs 3.11+.
	Python string
	// Runtime overrides the embedded Foundry package (tests).
	Runtime fs.FS
	// Env overrides the child's environment (tests). Default: os.Environ().
	Env []string
}

// Service owns at most one Foundry process. It starts lazily on the first request, so a
// missing interpreter or a broken Foundry never keeps the dashboard from opening.
type Service struct {
	opts    Options
	dataDir string

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{}
	adopted bool
	port    int
	token   string
	lastErr error
	lastTry time.Time
	closed  bool
}

// New validates the options. It starts nothing.
func New(opts Options) (*Service, error) {
	if opts.DataDir == "" {
		if opts.StateDir == "" {
			return nil, errors.New("foundryloop: StateDir or DataDir is required")
		}
		opts.DataDir = filepath.Join(opts.StateDir, "foundry-loop", "data")
	}
	if !filepath.IsAbs(opts.DataDir) {
		return nil, fmt.Errorf("foundryloop: data directory %q must be absolute", opts.DataDir)
	}
	if opts.Python == "" {
		opts.Python = "python3"
	}
	if opts.Runtime == nil {
		opts.Runtime = foundrybundle.Runtime()
	}
	if opts.Env == nil {
		opts.Env = os.Environ()
	}
	return &Service{opts: opts, dataDir: opts.DataDir}, nil
}

// DataDir reports where Foundry keeps its store.
func (s *Service) DataDir() string { return s.dataDir }

// target returns the loopback address and token of a running Foundry, starting it when
// needed. A failed start is remembered for retryAfter so a broken install is not
// re-executed on every request.
func (s *Service) target(ctx context.Context) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", "", errors.New("Workbench is shutting down")
	}
	if s.aliveLocked() {
		return s.addrLocked(), s.token, nil
	}
	if s.lastErr != nil && time.Since(s.lastTry) < retryAfter {
		return "", "", s.lastErr
	}
	s.lastTry = time.Now()
	if err := s.startLocked(ctx); err != nil {
		s.lastErr = err
		return "", "", err
	}
	s.lastErr = nil
	return s.addrLocked(), s.token, nil
}

func (s *Service) addrLocked() string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(s.port)) }

func (s *Service) aliveLocked() bool {
	if s.port == 0 {
		return false
	}
	if s.cmd != nil {
		select {
		case <-s.done:
			s.port = 0
			return false
		default:
			return true
		}
	}
	// An adopted process is not ours to wait on: ask it.
	if _, ok := probe(s.port); !ok {
		s.port = 0
		return false
	}
	return true
}

func (s *Service) startLocked(ctx context.Context) error {
	python, err := findPython(ctx, s.opts.Python)
	if err != nil {
		return err
	}
	runtimeDir, err := extractRuntime(s.opts.Runtime, filepath.Join(filepath.Dir(s.dataDir), "runtime"))
	if err != nil {
		return fmt.Errorf("unpack the Foundry runtime: %w", err)
	}
	if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
		return err
	}
	if port, token, ok := s.adoptable(); ok {
		s.cmd, s.done, s.adopted, s.port, s.token = nil, nil, true, port, token
		return nil
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(s.dataDir, "service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(python, "-m", "foundry_loop", "--data", s.dataDir, "start", "--port", strconv.Itoa(port))
	cmd.Dir = runtimeDir
	cmd.Env = childEnv(s.opts.Env, runtimeDir)
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = processAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Foundry: %w", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()

	deadline := time.NewTimer(startTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			killGroup(cmd, done)
			return ctx.Err()
		case <-deadline.C:
			killGroup(cmd, done)
			return fmt.Errorf("Foundry did not answer within %s%s", startTimeout, s.logTail())
		case <-done:
			return fmt.Errorf("Foundry exited while starting%s", s.logTail())
		case <-tick.C:
			if pid, ok := probe(port); ok && pid == cmd.Process.Pid {
				token, err := readToken(s.dataDir)
				if err != nil {
					killGroup(cmd, done)
					return fmt.Errorf("read the Foundry session token: %w", err)
				}
				s.cmd, s.done, s.adopted, s.port, s.token = cmd, done, false, port, token
				return nil
			}
		}
	}
}

// adoptable finds a Foundry that outlived a previous Workbench (same data directory,
// answering on the port recorded in server.json with the pid it recorded).
func (s *Service) adoptable() (int, string, bool) {
	raw, err := os.ReadFile(filepath.Join(s.dataDir, "server.json"))
	if err != nil {
		return 0, "", false
	}
	var meta struct{ Port, Pid int }
	if json.Unmarshal(raw, &meta) != nil || meta.Port <= 0 || meta.Pid <= 0 {
		return 0, "", false
	}
	if pid, ok := probe(meta.Port); !ok || pid != meta.Pid {
		return 0, "", false
	}
	token, err := readToken(s.dataDir)
	if err != nil {
		return 0, "", false
	}
	return meta.Port, token, true
}

// Close stops the Foundry this Service started (never an adopted one, which a previous
// run or another tool owns). Foundry is asked to shut down; a process still alive when
// the grace period ends is killed with its whole process group.
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	cmd, done, port, token, adopted := s.cmd, s.done, s.port, s.token, s.adopted
	s.cmd, s.port = nil, 0
	s.mu.Unlock()
	if cmd == nil || adopted {
		return nil
	}
	if port != 0 {
		_ = shutdownRequest(ctx, port, token)
	}
	grace := time.NewTimer(stopTimeout)
	defer grace.Stop()
	select {
	case <-done:
	case <-grace.C:
		killGroup(cmd, done)
	case <-ctx.Done():
		killGroup(cmd, done)
		return ctx.Err()
	}
	return nil
}

func (s *Service) markDown() {
	s.mu.Lock()
	s.port = 0
	s.mu.Unlock()
}

// logTail is the end of Foundry's own log, appended to start errors so the dashboard can
// say why instead of "failed".
func (s *Service) logTail() string {
	file, err := os.Open(filepath.Join(s.dataDir, "service.log"))
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	offset := max(info.Size()-logTailBytes, 0)
	buf := make([]byte, info.Size()-offset)
	if _, err := file.ReadAt(buf, offset); err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	text := strings.TrimSpace(string(bytes.ToValidUTF8(buf, nil)))
	if text == "" {
		return ""
	}
	return ": " + text
}

// pythonCandidates are tried, in order, when no interpreter is configured: servers often have
// an old python3 (3.9/3.10) beside an installed python3.11+.
var pythonCandidates = []string{"python3", "python3.14", "python3.13", "python3.12", "python3.11", "python"}

// findPython returns the interpreter to run Foundry with. An explicitly configured one
// (WORKBENCH_FOUNDRY_PYTHON) is the only one tried; otherwise the first candidate on PATH
// that is 3.11 or newer. The error says what was found, so an operator knows what to install.
func findPython(ctx context.Context, configured string) (string, error) {
	candidates := pythonCandidates
	if configured != "" {
		candidates = []string{configured}
	}
	var seen []string
	for _, name := range candidates {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		version, err := pythonVersion(ctx, path)
		if err != nil {
			seen = append(seen, fmt.Sprintf("%s (does not run: %v)", path, err))
			continue
		}
		if version >= minPython {
			return path, nil
		}
		seen = append(seen, fmt.Sprintf("%s is %d.%d", path, version/100, version%100))
	}
	if len(seen) == 0 {
		return "", fmt.Errorf("Foundry needs Python 3.11 or newer and found none (looked for %s on PATH; set WORKBENCH_FOUNDRY_PYTHON to an interpreter)", strings.Join(candidates, ", "))
	}
	return "", fmt.Errorf("Foundry needs Python 3.11 or newer; found %s (install a newer Python or set WORKBENCH_FOUNDRY_PYTHON)", strings.Join(seen, "; "))
}

// pythonVersion reports major*100+minor.
func pythonVersion(ctx context.Context, python string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, python, "-c", "import sys;print(sys.version_info[0]*100+sys.version_info[1])").Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// childEnv drops Workbench's own configuration (it can carry the access token) and makes
// the unpacked package importable. The user's HOME and PATH stay: Foundry runs the user's
// agents and reads their session folders.
func childEnv(base []string, runtimeDir string) []string {
	env := make([]string, 0, len(base)+2)
	for _, entry := range base {
		if strings.HasPrefix(entry, "WORKBENCH_") || strings.HasPrefix(entry, "PYTHONPATH=") || strings.HasPrefix(entry, "FOUNDRY_DATA=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "PYTHONPATH="+runtimeDir, "PYTHONUNBUFFERED=1")
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

var healthClient = &http.Client{Timeout: healthTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// probe asks Foundry's unauthenticated /health and returns its pid when it is Foundry.
func probe(port int) (int, bool) {
	response, err := healthClient.Get("http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/health")
	if err != nil {
		return 0, false
	}
	defer response.Body.Close()
	var health struct {
		Application string `json:"application"`
		Pid         int    `json:"pid"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health) != nil || health.Application != application {
		return 0, false
	}
	return health.Pid, true
}

// readToken reads the session token Foundry generated in its own store. The database is
// opened read-only; Foundry stays its only writer.
func readToken(dataDir string) (string, error) {
	dsn := (&url.URL{Scheme: "file", Path: filepath.Join(dataDir, "foundry.sqlite"), RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var raw string
	if err := db.QueryRow(`SELECT value FROM kv WHERE key='token'`).Scan(&raw); err != nil {
		return "", err
	}
	var token string
	if err := json.Unmarshal([]byte(raw), &token); err != nil || token == "" {
		return "", errors.New("the stored token is not a string")
	}
	return token, nil
}

func shutdownRequest(ctx context.Context, port int, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port))+"/api/action", strings.NewReader(`{"action":"shutdown"}`))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Foundry-Token", token)
	response, err := healthClient.Do(request)
	if err != nil {
		return err
	}
	return response.Body.Close()
}

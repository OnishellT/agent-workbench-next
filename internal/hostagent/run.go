package hostagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"workbench/internal/app"
	"workbench/internal/config"
	"workbench/internal/hosts"
	"workbench/internal/models"
	"workbench/internal/settings"
)

var (
	hostIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
	hashPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	noncePattern  = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

const (
	maxOwnerFile = 4096
	shutdownWait = 8 * time.Second
)

// Run starts the authenticated local helper subcommand. args contains only
// --state-dir, --host-id, and --ready-file; the access token is read from its
// private state file and is never accepted through argv or logged.
func Run(ctx context.Context, args []string, version string) error {
	if ctx == nil {
		return errors.New("host-agent requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}
	if !hostIDPattern.MatchString(opts.hostID) || opts.hostID == "local" {
		return errors.New("host-agent host-id inválido")
	}
	stateDir, err := filepath.Abs(opts.stateDir)
	if err != nil || filepath.Clean(stateDir) != stateDir || stateDir == string(filepath.Separator) {
		return errors.New("host-agent state-dir inválido")
	}
	readyFile, err := filepath.Abs(opts.readyFile)
	if err != nil || filepath.Clean(readyFile) != readyFile || readyFile != filepath.Join(stateDir, hosts.HostAgentReadyFile) {
		return errors.New("host-agent ready-file debe estar bajo state-dir")
	}
	if err := verifyPrivateState(stateDir); err != nil {
		return err
	}
	stateLock, err := config.LockStateDir(stateDir)
	if err != nil {
		return fmt.Errorf("host-agent state lock: %w", err)
	}
	defer stateLock.Close()
	owner, err := readOwner(filepath.Join(stateDir, hosts.HostAgentOwnerFile))
	if err != nil {
		return err
	}
	if owner.HostID != opts.hostID || owner.UID != os.Geteuid() || !hashPattern.MatchString(owner.Hash) || !noncePattern.MatchString(owner.Nonce) || filepath.Base(owner.Executable) != owner.Executable || owner.Executable == "" {
		return errors.New("host-agent ownership marker inválido")
	}
	executable := filepath.Join(stateDir, "bin", owner.Executable)
	if err := verifyExecutable(executable, owner.Hash); err != nil {
		return err
	}
	currentExecutable, err := os.Executable()
	if err != nil {
		return err
	}
	currentHash, err := hashFile(currentExecutable)
	if err != nil || currentHash != owner.Hash {
		return errors.New("host-agent binary no coincide con el marcador de instalación")
	}
	if err := verifyPrivateFile(filepath.Join(stateDir, hosts.HostAgentTokenFile), 0o600); err != nil {
		return err
	}
	if err := removeStaleReady(readyFile, owner); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("host-agent loopback listener: %w", err)
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	listen := net.JoinHostPort("127.0.0.1", fmt.Sprint(address.Port))
	cfg, err := config.Load([]string{"--state-dir", stateDir, "--listen", listen, "--public-url", "http://" + listen})
	if err != nil {
		return err
	}
	cfg.Version = version

	settingsStore, err := settings.Open(stateDir)
	if err != nil {
		return err
	}
	nativeHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// Dependency installation keeps Bun in the native user's own directory.
	// Preserve an explicitly configured executable; never download at startup.
	if os.Getenv("MAGPIE_BUN") == "" {
		bun := filepath.Join(nativeHome, ".bun", "bin", "bun")
		if info, statErr := os.Stat(bun); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			if err := os.Setenv("MAGPIE_BUN", bun); err != nil {
				return err
			}
			defer os.Unsetenv("MAGPIE_BUN")
		}
	}
	modelsModule, err := models.New(stateDir, cfg.PublicURL, cfg.Listen, nativeHome)
	if err != nil {
		return err
	}
	application, err := app.New(cfg, nil, app.Modules{
		Models:      modelsModule.Handler(),
		AgentModels: modelsModule.AgentHandler(),
		Settings:    settingsStore,
	})
	if err != nil {
		_ = modelsModule.Close(context.Background())
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), shutdownWait)
		defer cancel()
		_ = application.Close(closeCtx)
		_ = modelsModule.Close(closeCtx)
	}()

	server := &http.Server{Handler: application.Handler(), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 1 << 20}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	defer server.Close()
	if err := awaitServing(ctx, listen, serveDone); err != nil {
		return err
	}
	receipt := hosts.HostAgentReady{HostID: opts.hostID, UID: os.Geteuid(), Version: version, Hash: owner.Hash, Nonce: owner.Nonce, PID: os.Getpid(), Port: address.Port, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := writeReady(readyFile, receipt); err != nil {
		return err
	}
	defer os.Remove(readyFile)
	select {
	case err := <-serveDone:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("host-agent server: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownWait)
		defer cancel()
		appErr := application.Close(shutdownCtx)
		serverErr := server.Shutdown(shutdownCtx)
		if serverErr != nil {
			_ = server.Close()
		}
		if appErr != nil {
			return errors.Join(ctx.Err(), appErr, serverErr)
		}
		return errors.Join(ctx.Err(), serverErr)
	}
}

func awaitServing(ctx context.Context, address string, serverDone <-chan error) error {
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 250 * time.Millisecond, Transport: transport}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	url := "http://" + address + "/health"
	var lastErr error
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			lastErr = err
		} else {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusServiceUnavailable {
				lastErr = fmt.Errorf("health returned HTTP %d", response.StatusCode)
			} else {
				var health struct {
					Application string            `json:"application"`
					Modules     map[string]string `json:"modules"`
				}
				if json.Unmarshal(data, &health) != nil || health.Application != "agent-workbench" {
					lastErr = errors.New("health reply is not a Workbench report")
				} else if health.Modules["models"] == "mounted" && health.Modules["settings"] == "mounted" {
					return nil
				} else {
					lastErr = errors.New("required host-agent modules are not mounted")
				}
			}
		}
		select {
		case serverErr := <-serverDone:
			return fmt.Errorf("host-agent exited before readiness: %w", serverErr)
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if lastErr != nil {
				return fmt.Errorf("host-agent failed to become ready: %w", lastErr)
			}
			return errors.New("host-agent failed to become ready")
		case <-ticker.C:
		}
	}
}

type runOptions struct {
	stateDir, hostID, readyFile string
}

func parseArgs(args []string) (runOptions, error) {
	flags := flag.NewFlagSet("host-agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	stateDir := flags.String("state-dir", "", "private helper state directory")
	hostID := flags.String("host-id", "", "canonical host identifier")
	readyFile := flags.String("ready-file", "", "private ready receipt path")
	if err := flags.Parse(args); err != nil {
		return runOptions{}, err
	}
	if flags.NArg() != 0 || *stateDir == "" || *hostID == "" || *readyFile == "" {
		return runOptions{}, errors.New("host-agent requires state-dir, host-id, and ready-file")
	}
	return runOptions{stateDir: *stateDir, hostID: *hostID, readyFile: *readyFile}, nil
}

func verifyPrivateState(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || !ownedByCurrentUser(info) {
		return errors.New("host-agent state-dir debe ser un directorio privado 0700 propiedad del usuario")
	}
	if resolved, err := filepath.EvalSymlinks(dir); err != nil || resolved != dir {
		return errors.New("host-agent state-dir no puede atravesar symlinks")
	}
	return nil
}

func verifyPrivateFile(path string, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != mode || !ownedByCurrentUser(info) {
		return errors.New("host-agent private file has unsafe owner, type or permissions")
	}
	return nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func readOwner(path string) (hosts.HostAgentOwner, error) {
	var owner hosts.HostAgentOwner
	if err := verifyPrivateFile(path, 0o600); err != nil {
		return owner, err
	}
	file, err := os.Open(path)
	if err != nil {
		return owner, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxOwnerFile+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&owner); err != nil {
		return owner, errors.New("host-agent ownership marker is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return owner, errors.New("host-agent ownership marker has trailing data")
	}
	return owner, nil
}

func verifyExecutable(path, expectedHash string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || !ownedByCurrentUser(info) {
		return errors.New("host-agent executable has unsafe owner, type or permissions")
	}
	got, err := hashFile(path)
	if err != nil || got != expectedHash {
		return errors.New("host-agent executable hash mismatch")
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func removeStaleReady(path string, owner hosts.HostAgentOwner) error {
	if err := verifyPrivateFile(path, 0o600); err != nil {
		if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var ready hosts.HostAgentReady
	if json.Unmarshal(data, &ready) != nil || ready.HostID != owner.HostID || ready.UID != owner.UID || ready.Hash != owner.Hash || ready.Nonce != owner.Nonce || ready.PID < 1 || ready.Port < 1 || ready.Port > 65535 {
		return errors.New("host-agent refuses an unowned ready receipt")
	}
	if helperRunning(ready.PID, owner.Hash) {
		return errors.New("host-agent is already running for this owner")
	}
	return os.Remove(path)
}

// helperRunning reports whether pid is this owner's helper: alive and executing the
// binary whose hash the ownership marker names. A recycled PID (any other program, or
// one this user cannot inspect) is a stale receipt, not a running helper.
func helperRunning(pid int, ownerHash string) bool {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	got, err := hashFile("/proc/" + strconv.Itoa(pid) + "/exe")
	return err == nil && got == ownerHash
}

func writeReady(path string, ready hosts.HostAgentReady) error {
	data, err := json.Marshal(ready)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".agent-ready-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}

package hosts

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestMain(m *testing.M) {
	if os.Getenv("WORKBENCH_TEST_HOST_AGENT_PROCESS") == "1" {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
		<-signals
		signal.Stop(signals)
		if marker := os.Getenv("WORKBENCH_TEST_HOST_AGENT_MARKER"); marker != "" {
			if err := os.WriteFile(marker, []byte("stopping"), 0o600); err != nil {
				os.Exit(3)
			}
		}
		if delay, err := strconv.Atoi(os.Getenv("WORKBENCH_TEST_HOST_AGENT_DELAY_MS")); err == nil && delay > 0 {
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestHostAgentInstallUsesRevisionCASAndPrivateOwnedArtifacts(t *testing.T) {
	home := t.TempDir()
	address, port, pin, launched := hostAgentSSHFixture(t, home)
	m, _ := newTestModule(t)
	defer m.Close()
	host := trustedHostAgentFixture(t, m, address, port, pin)
	handler := m.HostsHandler()

	initial := moduleRequest(t, handler, http.MethodGet, "/api/hosts/fixture-agent/magpie/status", "")
	if initial.Code != http.StatusOK || !strings.Contains(initial.Body.String(), `"state":"absent"`) || strings.Contains(initial.Body.String(), `"ready":true`) {
		t.Fatalf("initial status must be absent and not ready: %d %s", initial.Code, initial.Body.String())
	}
	stateDir := filepath.Join(home, ".local", "state", "agent-workbench-next", "host-agent", host.ID)
	t.Setenv("WORKBENCH_MAGPIE_BIN", filepath.Join(t.TempDir(), "missing-magpie"))
	stale := moduleRequest(t, handler, http.MethodPost, "/api/hosts/fixture-agent/magpie/install", fmt.Sprintf(`{"revision":%d}`, host.Revision+1))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale revision install status = %d, want %d: %s", stale.Code, http.StatusConflict, stale.Body.String())
	}
	if _, err := os.Lstat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("stale CAS created remote state: %v", err)
	}

	setNativeMagpieTestBinary(t)

	installed := moduleRequest(t, handler, http.MethodPost, "/api/hosts/fixture-agent/magpie/install", fmt.Sprintf(`{"revision":%d}`, host.Revision))
	if installed.Code != http.StatusAccepted {
		t.Fatalf("fixture launch is expected to install but remain pending: %d %s", installed.Code, installed.Body.String())
	}
	var public hostAgentStatus
	if err := json.Unmarshal(installed.Body.Bytes(), &public); err != nil || !public.Installed || public.Ready || public.State != "installed" || public.HostID != host.ID || public.Revision != host.Revision {
		t.Fatalf("install response is not an honest installed/not-ready projection: %+v %v", public, err)
	}
	ownerData, err := os.ReadFile(filepath.Join(stateDir, HostAgentOwnerFile))
	if err != nil {
		t.Fatal(err)
	}
	var owner HostAgentOwner
	if err := json.Unmarshal(ownerData, &owner); err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(filepath.Join(stateDir, HostAgentTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(stateDir, "bin", owner.Executable)
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	binaryHash := sha256.Sum256(binary)
	if owner.HostID != host.ID || owner.UID != os.Geteuid() || owner.Nonce == "" || !hostAgentNoncePattern.MatchString(owner.Nonce) || owner.Hash != hex.EncodeToString(binaryHash[:]) || owner.Executable != hostAgentExecutable || !validHostAgentToken(token) {
		t.Fatalf("installed owner/token/binary did not satisfy the ownership contract: owner=%+v token_valid=%v", owner, validHostAgentToken(token))
	}
	for name, mode := range map[string]os.FileMode{
		stateDir:                       0o700,
		filepath.Join(stateDir, "bin"): 0o700,
		binaryPath:                     0o700,
		filepath.Join(stateDir, HostAgentOwnerFile): 0o600,
		filepath.Join(stateDir, HostAgentTokenFile): 0o600,
	} {
		info, err := os.Lstat(name)
		if err != nil || info.Mode().Perm() != mode || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("installed path %q mode/type = %v, err=%v; want private mode %04o", name, info, err, mode)
		}
	}
	select {
	case command := <-launched:
		if strings.Contains(command, string(token)) || strings.Contains(command, "access.token") {
			t.Fatalf("helper launch command exposed token or referenced it on argv: %q", command)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("host-agent launch command was not issued")
	}
	for _, private := range []string{string(token), owner.Nonce, owner.Hash} {
		if strings.Contains(installed.Body.String(), private) {
			t.Fatal("install response exposed an internal token/nonce/hash")
		}
	}
	status := moduleRequest(t, handler, http.MethodGet, "/api/hosts/fixture-agent/magpie/status", "")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"installed":true`) || !strings.Contains(status.Body.String(), `"ready":false`) {
		t.Fatalf("status must validate installation without claiming readiness: %d %s", status.Code, status.Body.String())
	}
	for _, private := range []string{string(token), owner.Nonce, owner.Hash} {
		if strings.Contains(status.Body.String(), private) {
			t.Fatal("status response exposed an internal token/nonce/hash")
		}
	}
}
func TestHostAgentUpgradeReplacesOnlyVerifiedOwnedInstall(t *testing.T) {
	setNativeMagpieTestBinary(t)
	type fixture struct {
		module    *Module
		host      Host
		stateDir  string
		owner     HostAgentOwner
		ownerData []byte
		token     []byte
		oldBinary []byte
		launched  <-chan string
	}
	setup := func(t *testing.T) fixture {
		t.Helper()
		home := t.TempDir()
		address, port, pin, launched := hostAgentSSHFixture(t, home)
		m, _ := newTestModule(t)
		t.Cleanup(func() { _ = m.Close() })
		host := trustedHostAgentFixture(t, m, address, port, pin)
		installed := moduleRequest(t, m.HostsHandler(), http.MethodPost, "/api/hosts/fixture-agent/magpie/install", fmt.Sprintf(`{"revision":%d}`, host.Revision))
		if installed.Code != http.StatusAccepted {
			t.Fatalf("initial helper install status = %d: %s", installed.Code, installed.Body.String())
		}
		select {
		case <-launched:
		case <-time.After(2 * time.Second):
			t.Fatal("initial helper launch missing")
		}
		stateDir := filepath.Join(home, ".local", "state", "agent-workbench-next", "host-agent", host.ID)
		ownerData, err := os.ReadFile(filepath.Join(stateDir, HostAgentOwnerFile))
		if err != nil {
			t.Fatal(err)
		}
		var owner HostAgentOwner
		if err := json.Unmarshal(ownerData, &owner); err != nil {
			t.Fatal(err)
		}
		token, err := os.ReadFile(filepath.Join(stateDir, HostAgentTokenFile))
		if err != nil {
			t.Fatal(err)
		}
		oldBinary, err := os.ReadFile(filepath.Join(stateDir, "bin", hostAgentExecutable))
		if err != nil {
			t.Fatal(err)
		}
		return fixture{module: m, host: host, stateDir: stateDir, owner: owner, ownerData: ownerData, token: token, oldBinary: oldBinary, launched: launched}
	}
	newRootBinary := func(t *testing.T, old []byte) hostAgentBinary {
		t.Helper()
		data := append(append([]byte(nil), old...), []byte("new-root-build")...)
		name := filepath.Join(t.TempDir(), hostAgentExecutable)
		writeFixtureFile(t, name, data, 0o700)
		file, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		hash := sha256.Sum256(data)
		return hostAgentBinary{file: file, size: int64(len(data)), hash: hex.EncodeToString(hash[:])}
	}
	writeReady := func(t *testing.T, f fixture, pid int) []byte {
		t.Helper()
		ready := HostAgentReady{HostID: f.host.ID, UID: os.Geteuid(), Version: "development", Hash: f.owner.Hash, Nonce: f.owner.Nonce, PID: pid, Port: 1, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		data := mustJSON(t, ready)
		writeFixtureFile(t, filepath.Join(f.stateDir, HostAgentReadyFile), data, 0o600)
		return data
	}
	update := func(t *testing.T, f fixture, binary hostAgentBinary) (remoteHostAgent, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var result remoteHostAgent
		err := f.module.SSH.SSHOperation(ctx, f.host.ID, func(opCtx context.Context, client *ssh.Client, identity Identity) error {
			var created bool
			var err error
			result, created, err = installRemoteHostAgent(opCtx, client, identity, binary)
			if created {
				return fmt.Errorf("upgrade incorrectly reported a new install")
			}
			return err
		})
		return result, err
	}

	t.Run("owned previous helper upgrades and preserves private state", func(t *testing.T) {
		f := setup(t)
		usagePath := filepath.Join(f.stateDir, "usage-history.fixture")
		writeFixtureFile(t, usagePath, []byte("preserve usage records"), 0o600)
		binaryPath := filepath.Join(f.stateDir, "bin", hostAgentExecutable)
		args := []string{"host-agent", "--state-dir", f.stateDir, "--host-id", f.host.ID, "--ready-file", filepath.Join(f.stateDir, HostAgentReadyFile)}
		cmd := exec.Command(binaryPath, args...)
		marker := filepath.Join(t.TempDir(), "term-received")
		cmd.Env = []string{"PATH=/usr/bin:/bin", "WORKBENCH_TEST_HOST_AGENT_PROCESS=1", "WORKBENCH_TEST_HOST_AGENT_MARKER=" + marker, "WORKBENCH_TEST_HOST_AGENT_DELAY_MS=400"}
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		waited := false
		t.Cleanup(func() {
			if !waited {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		processReady := false
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", cmd.Process.Pid))
			if err == nil && sameHostAgentArgs(cmdline, f.host.ID, f.stateDir) {
				processReady = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !processReady {
			t.Fatal("synthetic owned helper process did not start")
		}
		writeReady(t, f, cmd.Process.Pid)
		binary := newRootBinary(t, f.oldBinary)
		var result remoteHostAgent
		var err error
		type upgradeResult struct {
			state remoteHostAgent
			err   error
		}
		completed := make(chan upgradeResult, 1)
		go func() {
			state, err := update(t, f, binary)
			completed <- upgradeResult{state: state, err: err}
		}()
		stopping := false
		deadline = time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				stopping = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !stopping {
			t.Fatal("owned helper did not receive SIGTERM")
		}
		select {
		case got := <-completed:
			t.Fatalf("upgrade completed before the delayed owned helper exited: state=%+v err=%v", got.state.status, got.err)
		case <-time.After(100 * time.Millisecond):
		}
		if data, err := os.ReadFile(binaryPath); err != nil || string(data) != string(f.oldBinary) {
			t.Fatalf("binary replaced before old helper exited: err=%v", err)
		}
		if data, err := os.ReadFile(filepath.Join(f.stateDir, HostAgentOwnerFile)); err != nil || string(data) != string(f.ownerData) {
			t.Fatalf("owner marker replaced before old helper exited: err=%v", err)
		}
		select {
		case command := <-f.launched:
			t.Fatalf("new helper launched before old helper exited: %q", command)
		default:
		}
		select {
		case got := <-completed:
			result, err = got.state, got.err
		case <-time.After(5 * time.Second):
			t.Fatal("owned helper upgrade did not finish")
		}
		if err != nil {
			t.Fatalf("owned helper upgrade failed: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatalf("owned old helper did not stop cleanly: %v", err)
		}
		waited = true
		if !result.status.Installed || result.status.Ready || result.owner.Hash != binary.hash || result.owner.Nonce == f.owner.Nonce || result.owner.HostID != f.owner.HostID || result.owner.UID != f.owner.UID {
			t.Fatalf("upgrade result did not identify the new owner binary: %+v owner=%+v", result.status, result.owner)
		}
		for _, backup := range []string{
			filepath.Join(f.stateDir, "bin", hostAgentExecutable) + ".previous-" + result.owner.Nonce,
			filepath.Join(f.stateDir, HostAgentOwnerFile) + ".previous-" + result.owner.Nonce,
		} {
			if _, err := os.Stat(backup); err != nil {
				t.Fatalf("rollback backup removed before health was verified: %s: %v", filepath.Base(backup), err)
			}
		}
		gotBinary, err := os.ReadFile(filepath.Join(f.stateDir, "bin", hostAgentExecutable))
		if err != nil || !strings.HasSuffix(string(gotBinary), "new-root-build") {
			t.Fatalf("new root binary not installed: bytes=%d err=%v", len(gotBinary), err)
		}
		gotToken, err := os.ReadFile(filepath.Join(f.stateDir, HostAgentTokenFile))
		if err != nil || string(gotToken) != string(f.token) {
			t.Fatalf("private helper token changed: err=%v", err)
		}
		if data, err := os.ReadFile(usagePath); err != nil || string(data) != "preserve usage records" {
			t.Fatalf("private usage state changed: %q err=%v", data, err)
		}
		if _, err := os.Lstat(filepath.Join(f.stateDir, ".install.lock")); !os.IsNotExist(err) {
			t.Fatalf("upgrade lock left behind: %v", err)
		}
		select {
		case command := <-f.launched:
			if !strings.HasPrefix(command, "nohup ") || strings.Contains(command, string(f.token)) {
				t.Fatalf("unsafe helper launch after upgrade: %q", command)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("new helper launch missing")
		}
	})

	t.Run("live unowned PID does not get signaled or overwritten", func(t *testing.T) {
		f := setup(t)
		writeFixtureFile(t, filepath.Join(f.stateDir, "usage-history.fixture"), []byte("keep"), 0o600)
		readyData := writeReady(t, f, os.Getpid())
		binary := newRootBinary(t, f.oldBinary)
		_, err := update(t, f, binary)
		if !errors.Is(err, errHostAgentConflict) {
			t.Fatalf("unowned PID should fail closed, got %v", err)
		}
		if data, err := os.ReadFile(filepath.Join(f.stateDir, HostAgentOwnerFile)); err != nil || string(data) != string(f.ownerData) {
			t.Fatalf("owner marker changed on rejected update: err=%v", err)
		}
		if data, err := os.ReadFile(filepath.Join(f.stateDir, "bin", hostAgentExecutable)); err != nil || string(data) != string(f.oldBinary) {
			t.Fatalf("binary changed on rejected update: err=%v", err)
		}
		if data, err := os.ReadFile(filepath.Join(f.stateDir, HostAgentTokenFile)); err != nil || string(data) != string(f.token) {
			t.Fatalf("token changed on rejected update: err=%v", err)
		}
		if data, err := os.ReadFile(filepath.Join(f.stateDir, HostAgentReadyFile)); err != nil || string(data) != string(readyData) {
			t.Fatalf("unowned ready receipt changed on rejection: err=%v", err)
		}
		if _, err := os.Lstat(filepath.Join(f.stateDir, ".install.lock")); !os.IsNotExist(err) {
			t.Fatalf("rejected update left an install lock: %v", err)
		}
		select {
		case command := <-f.launched:
			t.Fatalf("rejected update launched a helper: %q", command)
		case <-time.After(50 * time.Millisecond):
		}
	})
}

func TestHostAgentStatusRejectsChangedOwnerUIDNonceHashAndToken(t *testing.T) {
	home := t.TempDir()
	address, port, pin, _ := hostAgentSSHFixture(t, home)
	m, _ := newTestModule(t)
	defer m.Close()
	host := trustedHostAgentFixture(t, m, address, port, pin)
	stateDir := filepath.Join(home, ".local", "state", "agent-workbench-next", "host-agent", host.ID)
	if err := writeReadyHostAgentFixture(stateDir, host.ID, os.Geteuid(), nil); err != nil {
		t.Fatal(err)
	}
	ownerPath := filepath.Join(stateDir, HostAgentOwnerFile)
	tokenPath := filepath.Join(stateDir, HostAgentTokenFile)
	binaryPath := filepath.Join(stateDir, "bin", hostAgentExecutable)
	ownerBytes, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	binaryBytes, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(t *testing.T)
	}{
		{name: "uid", mutate: func(t *testing.T) {
			var owner HostAgentOwner
			if err := json.Unmarshal(ownerBytes, &owner); err != nil {
				t.Fatal(err)
			}
			owner.UID++
			writeFixtureFile(t, ownerPath, mustJSON(t, owner), 0o600)
		}},
		{name: "nonce", mutate: func(t *testing.T) {
			var owner HostAgentOwner
			if err := json.Unmarshal(ownerBytes, &owner); err != nil {
				t.Fatal(err)
			}
			owner.Nonce = "not-a-valid-nonce"
			writeFixtureFile(t, ownerPath, mustJSON(t, owner), 0o600)
		}},
		{name: "hash", mutate: func(t *testing.T) {
			changed := sha256.Sum256([]byte("changed executable"))
			var owner HostAgentOwner
			if err := json.Unmarshal(ownerBytes, &owner); err != nil {
				t.Fatal(err)
			}
			owner.Hash = hex.EncodeToString(changed[:])
			writeFixtureFile(t, ownerPath, mustJSON(t, owner), 0o600)
		}},
		{name: "token", mutate: func(t *testing.T) { writeFixtureFile(t, tokenPath, []byte(strings.Repeat("x", 43)), 0o600) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeFixtureFile(t, ownerPath, ownerBytes, 0o600)
			writeFixtureFile(t, tokenPath, tokenBytes, 0o600)
			writeFixtureFile(t, binaryPath, binaryBytes, 0o700)
			tc.mutate(t)
			response := moduleRequest(t, m.HostsHandler(), http.MethodGet, "/api/hosts/fixture-agent/magpie/status", "")
			if response.Code != http.StatusConflict {
				t.Fatalf("tampered %s status = %d, want %d: %s", tc.name, response.Code, http.StatusConflict, response.Body.String())
			}
		})
	}
}

func TestHostAgentStatusAccepts503OnlyWithAllRequiredModules(t *testing.T) {
	home := t.TempDir()
	address, port, pin, _ := hostAgentSSHFixture(t, home)
	var fullHealth atomic.Bool
	fullHealth.Store(true)
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		degraded := "mounted"
		if !fullHealth.Load() {
			degraded = "pending"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"application": "agent-workbench", "modules": map[string]string{"settings": "mounted", "models": degraded}})
	}))
	defer health.Close()
	m, _ := newTestModule(t)
	defer m.Close()
	host := trustedHostAgentFixture(t, m, address, port, pin)
	stateDir := filepath.Join(home, ".local", "state", "agent-workbench-next", "host-agent", host.ID)
	if err := writeReadyHostAgentFixture(stateDir, host.ID, os.Geteuid(), health.Listener.Addr().(*net.TCPAddr)); err != nil {
		t.Fatal(err)
	}
	response := moduleRequest(t, m.HostsHandler(), http.MethodGet, "/api/hosts/fixture-agent/magpie/status", "")
	if response.Code != http.StatusOK {
		t.Fatalf("a healthy helper with root-only degradation was rejected: %d %s", response.Code, response.Body.String())
	}
	var got hostAgentStatus
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || !got.Installed || !got.Ready || got.State != "ready" {
		t.Fatalf("required-module health did not produce ready: %+v %v", got, err)
	}
	fullHealth.Store(false)
	degraded := moduleRequest(t, m.HostsHandler(), http.MethodGet, "/api/hosts/fixture-agent/magpie/status", "")
	if degraded.Code != http.StatusOK {
		t.Fatalf("degraded status request failed: %d %s", degraded.Code, degraded.Body.String())
	}
	if err := json.Unmarshal(degraded.Body.Bytes(), &got); err != nil || !got.Installed || got.Ready || got.State != "installed" {
		t.Fatalf("503 without all helper modules was incorrectly accepted as ready: %+v %v", got, err)
	}
}

func TestRemoteBinaryDigestUsesRemoteUtilityAndKeepsOwnershipChecks(t *testing.T) {
	home := t.TempDir()
	address, port, pin, _ := hostAgentSSHFixture(t, home)
	m, _ := newTestModule(t)
	defer m.Close()
	host := trustedHostAgentFixture(t, m, address, port, pin)
	dir := filepath.Join(home, "digest fixtures")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(strings.Repeat("remote digest fixture\n", 4096))
	name := filepath.Join(dir, "helper bin") // spaces must survive the shell quoting
	writeFixtureFile(t, name, data, 0o700)
	sum := sha256.Sum256(data)

	digest := func(t *testing.T, path string, uid int, mode os.FileMode) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var got string
		var digestErr error
		opErr := m.SSH.SSHOperation(ctx, host.ID, func(opCtx context.Context, client *ssh.Client, _ Identity) error {
			fs, err := sftp.NewClient(client)
			if err != nil {
				return err
			}
			defer fs.Close()
			got, digestErr = remoteBinaryDigest(opCtx, client, fs, path, uid, mode)
			return digestErr
		})
		if opErr != nil {
			return got, opErr
		}
		return got, digestErr
	}

	want := hex.EncodeToString(sum[:])
	if got, err := digest(t, name, os.Geteuid(), 0o700); err != nil || got != want {
		t.Fatalf("remote digest = %q, err=%v; want %q", got, err, want)
	}
	odd := filepath.Join(dir, "helper\nbin\\odd")
	oddData := []byte(strings.Repeat("odd filename fixture\n", 512))
	writeFixtureFile(t, odd, oddData, 0o700)
	oddSum := sha256.Sum256(oddData)
	if got, err := digest(t, odd, os.Geteuid(), 0o700); err != nil || got != hex.EncodeToString(oddSum[:]) {
		t.Fatalf("odd filename digest = %q, err=%v; want %q", got, err, hex.EncodeToString(oddSum[:]))
	}
	fakeDir := filepath.Join(home, "fake path")
	if err := os.MkdirAll(fakeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	used := filepath.Join(home, "fake-sha256sum-used")
	shim := "#!/bin/sh\nprintf used > " + shellQuote(used) + "\nprintf '%s  -\\n' " + strings.Repeat("0", 64) + "\n"
	writeFixtureFile(t, filepath.Join(fakeDir, "sha256sum"), []byte(shim), 0o700)
	writeFixtureFile(t, filepath.Join(home, ".fixture-remote-path"), []byte(fakeDir+":/usr/bin:/bin"), 0o600)
	if got, err := digest(t, name, os.Geteuid(), 0o700); err != nil || got != want {
		t.Fatalf("digest with a sha256sum earlier in PATH = %q, err=%v; want the system digest %q", got, err, want)
	}
	if _, err := os.Lstat(used); !os.IsNotExist(err) {
		t.Fatalf("a sha256sum from the remote PATH was executed: %v", err)
	}
	if err := os.Remove(filepath.Join(home, ".fixture-remote-path")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "helper link")
	if err := os.Symlink(name, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		path string
		mode os.FileMode
	}{
		{name: "symlink", path: link, mode: 0o700},
		{name: "unexpected mode", path: name, mode: 0o600},
		{name: "directory", path: dir, mode: 0o700},
	} {
		if got, err := digest(t, tc.path, os.Geteuid(), tc.mode); !errors.Is(err, errHostAgentConflict) || got != "" {
			t.Fatalf("%s digest = %q, err=%v; want a conflict without a hash", tc.name, got, err)
		}
	}
	if got, err := digest(t, filepath.Join(dir, "missing"), os.Geteuid(), 0o700); !errors.Is(err, os.ErrNotExist) || got != "" {
		t.Fatalf("missing digest = %q, err=%v; want a not-exist error without a hash", got, err)
	}
	for _, marker := range []string{".fixture-digest-fail", ".fixture-digest-garbage"} {
		writeFixtureFile(t, filepath.Join(home, marker), nil, 0o600)
		got, err := digest(t, name, os.Geteuid(), 0o700)
		if err == nil || got != "" {
			t.Fatalf("%s digest = %q, err=%v; a failing remote digest must never be a hash", marker, got, err)
		}
		if err := os.Remove(filepath.Join(home, marker)); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(home, ".fixture-digest-slow"), nil, 0o600)
	cancelledContext, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var cancelled string
	cancelErr := m.SSH.SSHOperation(cancelledContext, host.ID, func(opCtx context.Context, client *ssh.Client, _ Identity) error {
		fs, err := sftp.NewClient(client)
		if err != nil {
			return err
		}
		defer fs.Close()
		cancelled, err = remoteBinaryDigest(opCtx, client, fs, name, os.Geteuid(), 0o700)
		return err
	})
	if cancelErr == nil || cancelled != "" {
		t.Fatalf("cancelled digest = %q, err=%v; a cancelled request must not report a hash", cancelled, cancelErr)
	}
	if err := os.Remove(filepath.Join(home, ".fixture-digest-slow")); err != nil {
		t.Fatal(err)
	}
}

func TestParseRemoteDigestRejectsUnalignedOutput(t *testing.T) {
	valid := strings.Repeat("a", 64)
	cases := []struct {
		name string
		data string
		want string
	}{
		{name: "stdin summary", data: valid + "  -\n", want: valid},
		{name: "empty", data: ""},
		{name: "blank line", data: "\n"},
		{name: "utility warning", data: "sha256sum: no such file\n"},
		{name: "short digest", data: strings.Repeat("a", 63) + "  -\n"},
		{name: "uppercase digest", data: strings.ToUpper(valid) + "  -\n"},
		{name: "digest without stdin marker", data: valid + "\n"},
		{name: "digest with a filename", data: valid + "  /state/bin/workbench\n"},
		{name: "digest with an escaped filename", data: valid + "  /state/bin/helper\\nname\n"},
		{name: "two entries", data: valid + "  -\n" + valid + "  -\n"},
	}
	for _, tc := range cases {
		got, err := parseRemoteDigest([]byte(tc.data))
		if tc.want == "" {
			if err == nil || got != "" {
				t.Fatalf("%s parsed as %q, err=%v; want no digest", tc.name, got, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("%s parsed as %q, err=%v; want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestHostAgentStatusUsesRemoteDigestInsteadOfTransferringTheHelper(t *testing.T) {
	home := t.TempDir()
	address, port, pin, _ := hostAgentSSHFixture(t, home)
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"application": "agent-workbench", "modules": map[string]string{"settings": "mounted", "models": "mounted"}})
	}))
	defer health.Close()
	m, _ := newTestModule(t)
	defer m.Close()
	host := trustedHostAgentFixture(t, m, address, port, pin)
	stateDir := filepath.Join(home, ".local", "state", "agent-workbench-next", "host-agent", host.ID)
	if err := writeReadyHostAgentFixture(stateDir, host.ID, os.Geteuid(), health.Listener.Addr().(*net.TCPAddr)); err != nil {
		t.Fatal(err)
	}
	helper, err := os.Stat(filepath.Join(stateDir, "bin", hostAgentExecutable))
	if err != nil {
		t.Fatal(err)
	}
	if helper.Size() < 1<<20 {
		t.Fatalf("fixture helper is %d bytes; too small to prove the transfer change", helper.Size())
	}
	hostAgentFixtureSessionBytes.Store(0)
	response := moduleRequest(t, m.HostsHandler(), http.MethodGet, "/api/hosts/fixture-agent/magpie/status", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"installed":true`) || !strings.Contains(response.Body.String(), `"ready":true`) {
		t.Fatalf("status with remote digest = %d: %s", response.Code, response.Body.String())
	}
	moved := hostAgentFixtureSessionBytes.Load()
	if moved >= helper.Size()/2 {
		t.Fatalf("status inspect moved %d session bytes for a %d byte helper; the binary is still transferred", moved, helper.Size())
	}
	t.Logf("status inspect moved %d bytes for a %d byte helper", moved, helper.Size())
	for _, marker := range []string{".fixture-digest-fail", ".fixture-digest-garbage"} {
		writeFixtureFile(t, filepath.Join(home, marker), nil, 0o600)
		failed := moduleRequest(t, m.HostsHandler(), http.MethodGet, "/api/hosts/fixture-agent/magpie/status", "")
		if failed.Code != http.StatusConflict || strings.Contains(failed.Body.String(), `"installed":true`) {
			t.Fatalf("%s status = %d: %s", marker, failed.Code, failed.Body.String())
		}
		if err := os.Remove(filepath.Join(home, marker)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoteNativeMagpieReaderUsesRemoteDigestForTheNativeBinary(t *testing.T) {
	home := t.TempDir()
	address, port, pin, _ := hostAgentSSHFixture(t, home)
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"application": "agent-workbench", "modules": map[string]string{"settings": "mounted", "models": "mounted"}})
	}))
	defer health.Close()
	m, _ := newTestModule(t)
	defer m.Close()
	host := trustedHostAgentFixture(t, m, address, port, pin)
	stateDir := filepath.Join(home, ".local", "state", "agent-workbench-next", "host-agent", host.ID)
	if err := writeReadyHostAgentFixture(stateDir, host.ID, os.Geteuid(), health.Listener.Addr().(*net.TCPAddr)); err != nil {
		t.Fatal(err)
	}
	native := []byte(strings.Repeat("native magpie release fixture\n", 200000))
	nativeHash := sha256.Sum256(native)
	writeFixtureFile(t, filepath.Join(stateDir, "bin", "magpie"), native, 0o700)
	owner := nativeMagpieOwner{HostID: host.ID, UID: os.Geteuid(), Hash: hex.EncodeToString(nativeHash[:]), Nonce: "0123456789abcdef0123456789abcdef", Executable: "magpie"}
	writeFixtureFile(t, filepath.Join(stateDir, NativeMagpieOwnerFile), mustJSON(t, owner), 0o600)
	writeFixtureFile(t, filepath.Join(stateDir, "native-ports.json"), []byte(`{"site":45101,"web":45102,"gateway":45103}`), 0o600)
	writeFixtureFile(t, filepath.Join(stateDir, "native-web.key"), []byte(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))), 0o600)

	read := func(t *testing.T) (remoteNativeMagpieEndpoint, int64, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var endpoint remoteNativeMagpieEndpoint
		var moved int64
		err := m.SSH.SSHOperation(ctx, host.ID, func(opCtx context.Context, client *ssh.Client, identity Identity) error {
			fs, err := sftp.NewClient(client)
			if err != nil {
				return err
			}
			defer fs.Close()
			hostAgentFixtureSessionBytes.Store(0)
			endpoint, err = readRemoteNativeMagpieEndpoint(opCtx, client, identity, true)
			moved = hostAgentFixtureSessionBytes.Load()
			return err
		})
		return endpoint, moved, err
	}

	endpoint, moved, err := read(t)
	if err != nil || endpoint.port != 45102 || moved >= int64(len(native))/2 {
		t.Fatalf("native reader moved %d bytes for a %d byte native binary (port %d, err %v); the ELF is still transferred", moved, len(native), endpoint.port, err)
	}
	t.Logf("native reader moved %d bytes for a %d byte native binary", moved, len(native))

	writeFixtureFile(t, filepath.Join(stateDir, "bin", "magpie"), []byte(strings.Repeat("tampered native binary fixture\n", 200000)), 0o700)
	tampered, _, err := read(t)
	if !errors.Is(err, errHostAgentNotReady) || tampered.port != 0 {
		t.Fatalf("tampered native binary reader = %+v, err=%v; want not-ready without an endpoint", tampered, err)
	}
}

func writeReadyHostAgentFixture(stateDir, hostID string, uid int, listener *net.TCPAddr) error {
	binDir := filepath.Join(stateDir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return err
	}
	for _, dir := range []string{filepath.Dir(filepath.Dir(stateDir)), filepath.Dir(stateDir), stateDir, binDir} {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(binary)
	binaryPath := filepath.Join(binDir, hostAgentExecutable)
	if err := os.WriteFile(binaryPath, binary, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(binaryPath, 0o700); err != nil {
		return err
	}
	owner := HostAgentOwner{HostID: hostID, UID: uid, Hash: hex.EncodeToString(hash[:]), Nonce: "0123456789abcdef0123456789abcdef", Executable: hostAgentExecutable}
	ownerData, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stateDir, HostAgentOwnerFile), ownerData, 0o600); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := os.WriteFile(filepath.Join(stateDir, HostAgentTokenFile), []byte(token), 0o600); err != nil {
		return err
	}
	port := 45123
	if listener != nil {
		port = listener.Port
	}
	ready := HostAgentReady{HostID: hostID, UID: uid, Version: "fixture", Hash: owner.Hash, Nonce: owner.Nonce, PID: os.Getpid(), Port: port, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	readyData, err := json.Marshal(ready)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir, HostAgentReadyFile), readyData, 0o600)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeFixtureFile(t *testing.T, name string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(name, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, mode); err != nil {
		t.Fatal(err)
	}
}

func trustedHostAgentFixture(t *testing.T, m *Module, address string, port int, pin string) Host {
	t.Helper()
	host, err := m.SSH.Save(HostInput{Host: Host{ID: "fixture-agent", Address: address, Port: port, User: "dev", Auth: "password", Enabled: true}, Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	trustSSHFixture(t, m.SSH, ctx, &host, pin)
	return host
}

func setNativeMagpieTestBinary(t *testing.T) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "magpie")
	if err := os.WriteFile(name, []byte("owned native Magpie binary fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKBENCH_MAGPIE_BIN", name)
	return name
}

func hostAgentSSHFixture(t *testing.T, home string) (string, int, string, <-chan string) {
	return hostAgentSSHFixtureAt(t, home, "127.0.0.1:0", false)
}

func hostAgentSSHFixtureAt(t *testing.T, home, listenAddress string, executeCommands bool) (string, int, string, <-chan string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if c.User() != "dev" || string(password) != "fixture-secret" {
			return nil, fmt.Errorf("invalid fixture credentials")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	launched := make(chan string, 8)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					switch incoming.ChannelType() {
					case "direct-tcpip":
						go forwardHostAgentChannel(incoming)
					case "session":
						channel, requests, err := incoming.Accept()
						if err != nil {
							continue
						}
						go serveHostAgentSession(countingChannel{Channel: channel, bytes: &hostAgentFixtureSessionBytes}, requests, home, launched, executeCommands)
					default:
						_ = incoming.Reject(ssh.UnknownChannelType, "unsupported")
					}
				}
			}()
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, ssh.FingerprintSHA256(signer.PublicKey()), launched
}

func TestRemoteSSHFixtureForBrowser(t *testing.T) {
	if os.Getenv("WB_REMOTE_SSH_FIXTURE") != "1" {
		t.Skip("manual isolated browser fixture")
	}
	root := os.Getenv("WB_REMOTE_SSH_FIXTURE_ROOT")
	home := os.Getenv("WB_REMOTE_SSH_FIXTURE_HOME")
	if !filepath.IsAbs(root) || !filepath.IsAbs(home) || !strings.HasPrefix(filepath.Clean(home), filepath.Clean(root)+string(filepath.Separator)) {
		t.Fatal("remote fixture paths must be isolated absolute paths")
	}
	const hostID = "remote-magpie-fixture"
	listen := "127.0.0.1:18898"
	if override := os.Getenv("WB_REMOTE_SSH_FIXTURE_ADDR"); override != "" {
		listen = override
	}
	address, port, fingerprint, _ := hostAgentSSHFixtureAt(t, home, listen, true)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(map[string]any{
		"host_id": hostID, "address": address, "port": port, "user": "dev",
		"password": "fixture-secret", "fingerprint": fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ssh-fixture.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir, err := HostAgentStateDir(filepath.Join(home, ".local", "state"), hostID)
	if err != nil {
		t.Fatal(err)
	}
	usageDir := filepath.Join(stateDir, "agents", "home", ".config", "magpie")
	if err := os.MkdirAll(usageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	seed := []map[string]any{
		{"t": time.Now().UTC().Format(time.RFC3339Nano), "agent": "codex", "provider": "fixture-native", "model": "fixture-model", "req": "fixture-model", "in": 2222, "out": 444, "reasoning": 66, "session": "remote-fixture-1", "tries": 1},
		{"t": time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), "agent": "codex", "provider": "fixture-native", "model": "fixture-model", "req": "fixture-model", "in": 123, "out": 45, "reasoning": 7, "session": "remote-fixture-2", "tries": 1},
	}
	var ledger []byte
	for _, record := range seed {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		ledger = append(ledger, line...)
		ledger = append(ledger, '\n')
	}
	if err := os.WriteFile(filepath.Join(usageDir, "usage.jsonl"), ledger, 0o600); err != nil {
		t.Fatal(err)
	}
	fmt.Println("REMOTE_SSH_FIXTURE_READY", port)
	stopPath := filepath.Join(root, "stop")
	stop := time.NewTicker(250 * time.Millisecond)
	defer stop.Stop()
	for {
		if _, err := os.Stat(stopPath); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		<-stop.C
	}
}

var hostAgentFixtureDigestRuns atomic.Int64

func serveHostAgentSession(channel ssh.Channel, requests <-chan *ssh.Request, home string, launched chan<- string, executeCommands bool) {
	defer channel.Close()
	for request := range requests {
		switch request.Type {
		case "subsystem":
			var payload struct{ Name string }
			ssh.Unmarshal(request.Payload, &payload)
			if payload.Name != "sftp" {
				_ = request.Reply(false, nil)
				continue
			}
			_ = request.Reply(true, nil)
			server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(home))
			if err == nil {
				_ = server.Serve()
				_ = server.Close()
			}
			return
		case "exec":
			var payload struct{ Command string }
			ssh.Unmarshal(request.Payload, &payload)
			_ = request.Reply(true, nil)
			var output []byte
			status := uint32(0)
			switch {
			case strings.Contains(payload.Command, "hostname") && strings.Contains(payload.Command, "$HOME"):
				output = []byte(home + "\nfixture-remote\n")
			case strings.Contains(payload.Command, "$XDG_DATA_HOME"):
				for _, value := range []string{filepath.Join(home, ".local/share"), "", "", "", "", ""} {
					output = append(output, []byte(value+"\x00")...)
				}
			case strings.Contains(payload.Command, "${XDG_STATE_HOME"):
				output = []byte(filepath.Join(home, ".local/state") + "\x00" + strconv.Itoa(os.Geteuid()) + "\x00Linux\x00x86_64\x00")
			case strings.HasPrefix(payload.Command, "# workbench-owned-host-agent-stop\n"):
				if executeCommands {
					status = runFixtureRemoteCommand(home, payload.Command, &output)
				} else {
					command := exec.Command("sh", "-c", payload.Command)
					command.Env = []string{"PATH=/usr/bin:/bin"}
					commandOutput, commandErr := command.CombinedOutput()
					output = commandOutput
					if commandErr != nil {
						status = 1
					}
				}
			case strings.HasPrefix(payload.Command, "# workbench-owned-host-agent-digest\n"):
				hostAgentFixtureDigestRuns.Add(1)
				// A remote digest only needs a standard utility, so the fixture runs
				// it in both modes; markers force the negative cases.
				switch {
				case fixtureHomeFile(home, ".fixture-digest-fail"):
					status = 1
				case fixtureHomeFile(home, ".fixture-digest-garbage"):
					output = []byte("not-a-sha256-digest\n")
				case fixtureHomeFile(home, ".fixture-digest-slow"):
					stall := exec.Command("sh", "-c", "sleep 3")
					stall.Env = []string{"PATH=/usr/bin:/bin"}
					_ = stall.Run()
					status = runFixtureRemoteCommand(home, payload.Command, &output)
				default:
					status = runFixtureRemoteCommand(home, payload.Command, &output)
				}
			case strings.HasPrefix(payload.Command, "# workbench-owned-install-clock\n"):
				// The install-lock clock is a plain date read on the same machine
				// whose SFTP mtime the server compares it with.
				output = []byte(strconv.FormatInt(time.Now().Unix(), 10) + "\n")
			case strings.HasPrefix(payload.Command, "nohup "):
				select {
				case launched <- payload.Command:
				default:
				}
				if executeCommands {
					status = runFixtureRemoteCommand(home, payload.Command, nil)
				}
			default:
				if executeCommands {
					status = runFixtureRemoteCommand(home, payload.Command, &output)
				} else {
					status = 1
				}
			}
			if len(output) > 0 {
				_, _ = channel.Write(output)
			}
			_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
			return
		default:
			_ = request.Reply(false, nil)
		}
	}
}

func forwardHostAgentChannel(incoming ssh.NewChannel) {
	var payload struct {
		Address    string
		Port       uint32
		Origin     string
		OriginPort uint32
	}
	if err := ssh.Unmarshal(incoming.ExtraData(), &payload); err != nil {
		_ = incoming.Reject(ssh.ConnectionFailed, "invalid fixture channel")
		return
	}
	remote, err := net.DialTimeout("tcp", net.JoinHostPort(payload.Address, strconv.Itoa(int(payload.Port))), time.Second)
	if err != nil {
		_ = incoming.Reject(ssh.ConnectionFailed, "fixture target unavailable")
		return
	}
	channel, requests, err := incoming.Accept()
	if err != nil {
		_ = remote.Close()
		return
	}
	go ssh.DiscardRequests(requests)
	go func() {
		defer channel.Close()
		defer remote.Close()
		copyDone := make(chan struct{}, 1)
		go func() { _, _ = io.Copy(remote, channel); copyDone <- struct{}{} }()
		_, _ = io.Copy(channel, remote)
		<-copyDone
	}()
}

var _ = path.Join

// hostAgentFixtureSessionBytes records the SSH session traffic (SFTP and exec)
// the fixture served, so tests can assert the helper binary is no longer
// transferred on every inspect.
var hostAgentFixtureSessionBytes atomic.Int64

type countingChannel struct {
	ssh.Channel
	bytes *atomic.Int64
}

func (c countingChannel) Read(p []byte) (int, error) {
	n, err := c.Channel.Read(p)
	c.bytes.Add(int64(n))
	return n, err
}

func (c countingChannel) Write(p []byte) (int, error) {
	n, err := c.Channel.Write(p)
	c.bytes.Add(int64(n))
	return n, err
}

func fixtureHomeFile(home, name string) bool {
	_, err := os.Stat(filepath.Join(home, name))
	return err == nil
}

func runFixtureRemoteCommand(home, command string, output *[]byte) uint32 {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = home
	remotePath := "/usr/bin:/bin"
	if data, err := os.ReadFile(filepath.Join(home, ".fixture-remote-path")); err == nil {
		remotePath = strings.TrimSpace(string(data))
	}
	cmd.Env = []string{
		"HOME=" + home,
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"PATH=" + remotePath,
		"LANG=C",
	}
	if output != nil {
		var stdout strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = io.Discard
		if err := cmd.Run(); err != nil {
			return 1
		}
		*output = []byte(stdout.String())
		return 0
	}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

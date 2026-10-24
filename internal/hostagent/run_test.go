package hostagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"workbench/internal/hosts"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(serveMagpieFixture(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func serveMagpieFixture(args []string) int {
	addr := ""
	for _, arg := range args {
		if strings.HasPrefix(arg, "--web=") {
			addr = strings.TrimPrefix(arg, "--web=")
		}
	}
	if addr == "" || os.Getenv("MAGPIE_WEB_KEY") == "" {
		return 2
	}
	port := addr[strings.LastIndex(addr, ":")+1:]
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return 1
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != addr || r.Header.Get("Origin") != "http://"+addr || r.Header.Get("Cookie") != "magpie_web_"+port+"="+os.Getenv("MAGPIE_WEB_KEY") {
			http.Error(w, "unauthorized fixture request", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><title>magpie fixture</title>")
	})
	if (&http.Server{Handler: handler}).Serve(listener) != nil {
		return 1
	}
	return 0
}

func TestRunPublishesOwnedLoopbackReadyAndStops(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	stateDir := filepath.Join(root, "private-state")
	binDir := filepath.Join(stateDir, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	current, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKBENCH_MAGPIE_BIN", current)
	binary, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	hash := hex.EncodeToString(sum[:])
	executable := filepath.Base(current)
	if err := os.WriteFile(filepath.Join(binDir, executable), binary, 0700); err != nil {
		t.Fatal(err)
	}
	owner := hosts.HostAgentOwner{HostID: "fixture-host", UID: os.Geteuid(), Hash: hash, Nonce: "0123456789abcdef0123456789abcdef", Executable: executable}
	ownerBytes, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, hosts.HostAgentOwnerFile), ownerBytes, 0600); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("fixtureToken_", 4)
	if err := os.WriteFile(filepath.Join(stateDir, hosts.HostAgentTokenFile), []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	readyFile := filepath.Join(stateDir, hosts.HostAgentReadyFile)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []string{"--state-dir", stateDir, "--host-id", owner.HostID, "--ready-file", readyFile}, "fixture-version")
	}()

	var ready hosts.HostAgentReady
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for {
		data, readErr := os.ReadFile(readyFile)
		if readErr == nil {
			if err := json.Unmarshal(data, &ready); err != nil {
				t.Fatal(err)
			}
			break
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			t.Fatal(readErr)
		}
		select {
		case err := <-done:
			t.Fatalf("host-agent exited before ready: %v", err)
		case <-deadline.C:
			t.Fatal("host-agent did not publish ready")
		case <-poll.C:
		}
	}
	if ready.HostID != owner.HostID || ready.UID != owner.UID || ready.Hash != owner.Hash || ready.Nonce != owner.Nonce || ready.Version != "fixture-version" || ready.PID != os.Getpid() || ready.Port < 1 || ready.Port > 65535 {
		t.Fatalf("ready receipt does not match the private owner marker: %+v", ready)
	}
	for _, name := range []string{"native-ports.json", "native-web.key"} {
		info, err := os.Lstat(filepath.Join(stateDir, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("native state %s must be a private regular file at the host-agent root: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(stateDir, "models", "native-ports.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native state must not use a second models directory: %v", err)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(ready.Port)
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	healthBody, healthErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
	var health struct {
		Application string            `json:"application"`
		Modules     map[string]string `json:"modules"`
	}
	if healthErr != nil || (response.StatusCode != http.StatusOK && response.StatusCode != http.StatusServiceUnavailable) || json.Unmarshal(healthBody, &health) != nil || health.Application != "agent-workbench" || health.Modules["settings"] != "mounted" || health.Modules["models"] != "mounted" {
		t.Fatalf("host-agent health lacks required modules: status=%d body=%s err=%v", response.StatusCode, healthBody, healthErr)
	}
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, base+"/api/auth", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", base)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	responseBody, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 || bytes.Contains(responseBody, []byte(token)) {
		t.Fatalf("helper session exchange failed or exposed its token: status=%d cookies=%d body=%q err=%v", response.StatusCode, len(response.Cookies()), responseBody, readErr)
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("host-agent cancellation returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host-agent did not stop after cancellation")
	}
	if _, err := os.Lstat(readyFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ready receipt remained after stop: %v", err)
	}
}

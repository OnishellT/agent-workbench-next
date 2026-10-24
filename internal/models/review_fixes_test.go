package models

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestPublicCallerValidationIsCachedBriefly(t *testing.T) {
	const key = "fixture-native-public-key-0123456789"
	var roundTrips atomic.Int32
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		roundTrips.Add(1)
		switch r.URL.Path {
		case "/api/caller-keys":
			_, _ = w.Write([]byte(`{"keys":[{"id":"public","masked":"…6789"}]}`))
		case "/api/caller-keys/copy-key":
			_, _ = w.Write([]byte(`{"secret":"` + key + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer web.Close()
	u, _ := url.Parse(web.URL)
	port, _ := strconv.Atoi(u.Port())
	p := &nativeProcess{webURL: web.URL, webHost: u.Host, webKey: "k", ports: nativePorts{Web: port}, client: web.Client()}
	for range 3 {
		if ok, err := p.validPublicCaller(context.Background(), key); err != nil || !ok {
			t.Fatalf("valid = %v, %v", ok, err)
		}
	}
	if got := roundTrips.Load(); got != 2 {
		t.Fatalf("Magpie round trips = %d, want 2 for three requests", got)
	}
	for _, expiry := range p.callerCache {
		if time.Until(expiry) > callerCacheTTL {
			t.Fatal("cache entry outlives its TTL")
		}
	}
	for i := range callerCacheSize * 2 {
		p.callerAccepted("candidate-"+strconv.Itoa(i), true)
	}
	if len(p.callerCache) > callerCacheSize {
		t.Fatalf("cache grew to %d", len(p.callerCache))
	}
	// An expired entry no longer validates.
	p.callerAccepted(key, true)
	for digest := range p.callerCache {
		p.callerCache[digest] = time.Now().Add(-time.Second)
	}
	if p.callerAccepted(key, false) {
		t.Fatal("expired cache entry accepted")
	}
}

func TestWebKeyCreateRaceUsesTheKeyOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-web.key")
	if err := writePrivateFile(path, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(path, []byte("y")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second create = %v, want ErrExist", err)
	}
	path = filepath.Join(t.TempDir(), "state", "native-web.key")
	keys := make([]string, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if keys[i], err = loadNativeWebKey(path); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, key := range keys {
		if key != string(disk) {
			t.Fatalf("starter %d got %q, disk has %q", i, key, disk)
		}
	}
}

func TestPrivateReadsRejectSymlinksAndLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("0123456789012345678901234567890123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadNativeWebKey(link); err == nil {
		t.Fatal("followed a symlinked key")
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadNativeWebKey(target); err == nil {
		t.Fatal("accepted a group-readable key")
	}
}

func TestCloseSignalsTheWholeProcessGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 60 & echo $! ; wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, _ := out.Read(buf)
	grandchild, err := strconv.Atoi(string(buf[:n-1]))
	if err != nil {
		t.Fatalf("grandchild pid: %v (%q)", err, buf[:n])
	}
	p := &nativeProcess{cmd: cmd, group: true, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	if err := p.forceStop(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(grandchild, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("grandchild survived the group kill")
}

func TestHandlerDoesNotProxyUnknownPathsWithTheWebKey(t *testing.T) {
	reached := false
	web := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer web.Close()
	u, _ := url.Parse(web.URL)
	port, _ := strconv.Atoi(u.Port())
	m := &Module{native: &nativeProcess{webURL: web.URL, webHost: u.Host, webKey: "k", ports: nativePorts{Web: port}, client: web.Client()}}
	for _, path := range []string{"/api/models/catalog", "/api/state", "/"} {
		w := httptest.NewRecorder()
		m.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, w.Code)
		}
	}
	if reached {
		t.Fatal("an unknown path reached Magpie's web surface")
	}
}

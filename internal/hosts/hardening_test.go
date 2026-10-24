package hosts

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// sshPair connects an x/crypto client to an in-process server. reply decides
// whether the server answers global requests (keepalives).
func sshPair(t *testing.T, reply bool) *ssh.Client {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_, channels, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		if reply {
			go ssh.DiscardRequests(requests)
		}
		for range channels {
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "x", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func waitClosed(client *ssh.Client, within time.Duration) bool {
	done := make(chan struct{})
	go func() { _ = client.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(within):
		return false
	}
}

func TestKeepaliveClosesClientWhenServerNeverReplies(t *testing.T) {
	old := sshKeepaliveInterval
	sshKeepaliveInterval = 30 * time.Millisecond
	t.Cleanup(func() { sshKeepaliveInterval = old })
	client := sshPair(t, false)
	stop := startSSHKeepalive(context.Background(), client)
	defer stop()
	if !waitClosed(client, 3*time.Second) {
		t.Fatal("a dead transport must be closed by the keepalive")
	}
}

func TestKeepaliveKeepsLiveClientOpenAndStops(t *testing.T) {
	old := sshKeepaliveInterval
	sshKeepaliveInterval = 20 * time.Millisecond
	t.Cleanup(func() { sshKeepaliveInterval = old })
	client := sshPair(t, true)
	stop := startSSHKeepalive(context.Background(), client)
	if waitClosed(client, 300*time.Millisecond) {
		t.Fatal("a responsive server (even replying false) must not be closed")
	}
	stop()
}

func TestInstallContextExpiryClosesTheSSHClient(t *testing.T) {
	home := t.TempDir()
	address, port, pin, _ := hostAgentSSHFixture(t, home)
	m, _ := newTestModule(t)
	host := trustedHostAgentFixture(t, m, address, port, pin)
	binary := newTestBinary(t, []byte("payload"))
	var closed bool
	err := m.SSH.SSHOperation(context.Background(), host.ID, func(opCtx context.Context, client *ssh.Client, identity Identity) error {
		installCtx, cancel := context.WithTimeout(opCtx, time.Nanosecond)
		defer cancel()
		<-installCtx.Done()
		_, _, err := installRemoteHostAgent(installCtx, client, identity, binary)
		if err == nil {
			t.Error("an expired install context must fail the install")
		}
		closed = waitClosed(client, 2*time.Second)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("install timeout must close the SSH client, not only the steps around it")
	}
}

func newTestBinary(t *testing.T, data []byte) hostAgentBinary {
	t.Helper()
	name := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(name, data, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return hostAgentBinary{file: file, size: int64(len(data)), hash: "0"}
}

func TestUploadHostAgentBinaryStopsWhenContextEnds(t *testing.T) {
	home := t.TempDir()
	address, port, pin, _ := hostAgentSSHFixture(t, home)
	m, _ := newTestModule(t)
	host := trustedHostAgentFixture(t, m, address, port, pin)
	data := make([]byte, 1<<20)
	binary := newTestBinary(t, data)
	err := m.SSH.SSHOperation(context.Background(), host.ID, func(opCtx context.Context, client *ssh.Client, _ Identity) error {
		fs, err := sftp.NewClient(client)
		if err != nil {
			return err
		}
		defer fs.Close()
		ctx, cancel := context.WithCancel(opCtx)
		cancel()
		target := filepath.Join(home, "helper")
		err = uploadHostAgentBinary(ctx, binary.file, binary.size, binary.hash, fs, target, strings.Repeat("a", 32), os.Geteuid())
		if !errors.Is(err, context.Canceled) {
			t.Errorf("upload with an ended context = %v, want context.Canceled", err)
		}
		if _, statErr := os.Stat(target + ".tmp-" + strings.Repeat("a", 32)); !os.IsNotExist(statErr) {
			t.Errorf("partial upload left behind: %v", statErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCtxReaderAbortsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := ctxReader{ctx, strings.NewReader("abcdef")}
	buf := make([]byte, 3)
	if n, err := reader.Read(buf); n != 3 || err != nil {
		t.Fatalf("first read: %d %v", n, err)
	}
	cancel()
	if _, err := reader.Read(buf); !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancel: %v", err)
	}
}

func TestConversationDeadlineIsPerOperation(t *testing.T) {
	old := conversationOpTimeout
	conversationOpTimeout = 120 * time.Millisecond
	t.Cleanup(func() { conversationOpTimeout = old })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	read := &conversationSSH{ctx: ctx, cancel: cancel}
	// Three operations of ~70ms each: 210ms in total exceeds the 120ms budget,
	// but every one of them stays inside its own fresh deadline.
	for i := 0; i < 3; i++ {
		disarm := read.guard()
		time.Sleep(70 * time.Millisecond)
		disarm()
	}
	if read.ctxErr() != nil || read.opErr(errConversationRemote) != errConversationRemote {
		t.Fatalf("short operations must not trip the watchdog: %v", read.ctxErr())
	}
	disarm := read.guard()
	defer disarm()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a stuck operation must cancel the read")
	}
	if !errors.Is(read.ctxErr(), context.DeadlineExceeded) || !errors.Is(read.opErr(errConversationRemote), context.DeadlineExceeded) {
		t.Fatalf("a timeout must surface as DeadlineExceeded, got %v / %v", read.ctxErr(), read.opErr(errConversationRemote))
	}
}

func TestHistorySyncSlotHonoursContext(t *testing.T) {
	state := &hostHistoryState{sync: make(chan struct{}, 1)}
	if !state.acquireSync(context.Background()) {
		t.Fatal("free slot")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if state.acquireSync(ctx) {
		t.Fatal("a queued manual sync must give up when its request context ends")
	}
	if time.Since(start) > time.Second {
		t.Fatal("acquire did not return promptly")
	}
	if state.tryAcquireSync() {
		t.Fatal("auto capture must not run beside a manual sync")
	}
	state.releaseSync()
	if !state.tryAcquireSync() {
		t.Fatal("slot free after release")
	}
	state.releaseSync()
}

func TestReleaseInstallStateLogsInsteadOfFailing(t *testing.T) {
	home := t.TempDir()
	address, port, pin, _ := hostAgentSSHFixture(t, home)
	m, _ := newTestModule(t)
	host := trustedHostAgentFixture(t, m, address, port, pin)
	lock := filepath.Join(home, "lock")
	marker := filepath.Join(home, "marker")
	// A non-empty directory cannot be removed with SFTP REMOVE: the lock removal fails.
	if err := os.MkdirAll(filepath.Join(lock, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := m.SSH.SSHOperation(context.Background(), host.ID, func(opCtx context.Context, client *ssh.Client, _ Identity) error {
		fs, err := sftp.NewClient(client)
		if err != nil {
			return err
		}
		defer fs.Close()
		if releaseInstallState(fs, host.ID, lock, marker) {
			t.Error("lock removal failure must be reported to the caller as still held")
		}
		if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
			t.Error("the marker is still removed")
		}
		if !releaseInstallState(fs, host.ID, filepath.Join(home, "absent"), marker) {
			t.Error("an already-missing lock counts as released")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAllowedKeyPathRestrictsToSSHDirAndStateDir(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "id_ed25519")
	inside := filepath.Join(home, ".ssh", "id_ed25519")
	inState := filepath.Join(state, "key")
	for _, name := range []string{outside, inside, inState} {
		if err := os.WriteFile(name, []byte("k"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(home, ".ssh", "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	m := &SSHManager{Store: &Store{Dir: state}}
	for _, ok := range []string{inside, inState} {
		if _, err := m.allowedKeyPath(ok, true); err != nil {
			t.Errorf("%s should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{outside, link, "relative/key", "", filepath.Join(home, ".ssh") + "/../../etc/passwd", filepath.Join(home, ".ssh")} {
		if _, err := m.allowedKeyPath(bad, true); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	// A key that is generated after registering the host is accepted lexically at Save.
	if _, err := m.allowedKeyPath(filepath.Join(home, ".ssh", "later"), false); err != nil {
		t.Errorf("non-existing key under ~/.ssh at save time: %v", err)
	}
	if _, err := m.allowedKeyPath(filepath.Join(home, ".ssh", "later"), true); err == nil {
		t.Error("connect requires the file to exist")
	}
	if _, err := m.allowedKeyPath(filepath.Join(os.TempDir(), "nope-key"), false); err == nil {
		t.Error("lexically outside paths are rejected at save")
	}
}

func TestSaveRejectsKeyOutsideAllowedRoots(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m, _ := newTestModule(t)
	_, err := m.SSH.Save(HostInput{Host: Host{ID: "keyhost", Address: "127.0.0.1", User: "dev", Port: 22, Auth: "key", KeyPath: "/etc/shadow", Enabled: true}})
	if err == nil || !strings.Contains(err.Error(), "~/.ssh") {
		t.Fatalf("key path outside the allowlist accepted: %v", err)
	}
}

func TestHostAgentInspectCacheKeysOnIdentityAndExpires(t *testing.T) {
	var cache hostAgentInspectCache
	token := []byte("0123456789012345678901234567890123456789012")
	if !validHostAgentToken(token) {
		token = []byte(strings.Repeat("A", 43))
	}
	id := Identity{HostID: "h", Revision: 1, Digest: "d1"}
	state := remoteHostAgent{status: hostAgentStatus{Ready: true}, ready: HostAgentReady{Port: 9}, token: token}
	cache.put(id, state)
	got, ok := cache.get(id)
	if !ok || got.ready.Port != 9 {
		t.Fatal("same identity must hit")
	}
	clear(got.token)
	if again, ok := cache.get(id); !ok || string(again.token) != string(token) {
		t.Fatal("callers clearing their copy must not corrupt the cache")
	}
	if _, ok := cache.get(Identity{HostID: "h", Revision: 2, Digest: "d2"}); ok {
		t.Fatal("a new revision or fingerprint must miss")
	}
	cache.put(id, state)
	old := hostAgentInspectTTL
	hostAgentInspectTTL = -time.Second
	cache.put(id, state)
	hostAgentInspectTTL = old
	if _, ok := cache.get(id); ok {
		t.Fatal("expired entries must miss")
	}
	cache.put(id, remoteHostAgent{status: hostAgentStatus{Ready: false}})
	if _, ok := cache.get(id); ok {
		t.Fatal("only ready inspections are cached")
	}
}

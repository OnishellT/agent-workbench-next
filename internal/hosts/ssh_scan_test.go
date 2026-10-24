package hosts

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
)

// scanRemoteSource must report the same counts and statuses the inline scan did
// for every source shape: missing path, single file, jsonl tree, opencode dbs.
func TestScanRemoteSource(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	single := write("single.jsonl")
	write("tree/a.jsonl")
	write("tree/deep/b.jsonl")
	write("tree/ignored.txt")
	if err := os.Symlink(single, filepath.Join(root, "tree", "link.jsonl")); err != nil {
		t.Fatal(err)
	}
	write("oc/opencode.db")
	write("oc/opencode-2.db")
	write("oc/other.db")
	write("oc/opencode.dir.db/inner")

	clientConn, serverConn := pipePair()
	server, err := sftp.NewServer(serverConn, sftp.ReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	t.Cleanup(func() { _ = server.Close() })
	fs, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	for _, c := range []struct {
		name   string
		source Source
		count  int
		status string
	}{
		{"missing", Source{Kind: "codex", Path: filepath.Join(root, "nope"), Status: "connected"}, 0, "missing"},
		{"single file", Source{Kind: "codex", Path: single, Status: "connected"}, 1, "connected"},
		{"jsonl tree", Source{Kind: "codex", Path: filepath.Join(root, "tree"), Status: "connected"}, 2, "connected"},
		{"opencode", Source{Kind: "opencode", Path: filepath.Join(root, "oc"), Status: "connected"}, 2, "connected"},
	} {
		got, err := scanRemoteSource(context.Background(), fs, &c.source)
		if err != nil || got != c.count || c.source.Status != c.status || c.source.Error != "" {
			t.Fatalf("%s: count=%d err=%v status=%q error=%q; want %d %q", c.name, got, err, c.source.Status, c.source.Error, c.count, c.status)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := Source{Kind: "codex", Path: filepath.Join(root, "tree"), Status: "connected"}
	if _, err := scanRemoteSource(ctx, fs, &source); err == nil {
		t.Fatal("cancelled context must abort the walk")
	}
}

type pipeEnd struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p pipeEnd) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p pipeEnd) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p pipeEnd) Close() error                { _ = p.r.Close(); return p.w.Close() }

func pipePair() (client, server pipeEnd) {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	return pipeEnd{cr, cw}, pipeEnd{sr, sw}
}

// The probe commands are matched byte for byte by remote fixtures and hosts;
// the shared constants must equal the strings both callers used to inline.
func TestRemoteProbeCommandsUnchanged(t *testing.T) {
	if remoteIdentityCommand != "printf '%s\\n' \"$HOME\"; hostname 2>/dev/null || uname -n" {
		t.Fatalf("identity command changed: %q", remoteIdentityCommand)
	}
	if remoteEnvironmentCommand != "printf '%s\\0' \"$XDG_DATA_HOME\" \"$CODEX_HOME\" \"$OMP_AGENT_DIR\" \"$PI_CODING_AGENT_DIR\" \"$PI_CONFIG_DIR\" \"$CLAUDE_CONFIG_DIR\"" {
		t.Fatalf("environment command changed: %q", remoteEnvironmentCommand)
	}
}

func TestParseRemoteEnvironment(t *testing.T) {
	got, err := parseRemoteEnvironment([]byte("a\x00\x00c\x00\x00\x00f\x00"))
	if err != nil || len(got) != 6 || got[0] != "a" || got[2] != "c" || got[5] != "f" {
		t.Fatalf("valid output: %q %v", got, err)
	}
	for _, bad := range []string{"", "a\x00b\x00", "1\x002\x003\x004\x005\x006", "x\ny\x00\x00\x00\x00\x00\x00", "1\x002\x003\x004\x005\x006\x00x"} {
		if _, err := parseRemoteEnvironment([]byte(bad)); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

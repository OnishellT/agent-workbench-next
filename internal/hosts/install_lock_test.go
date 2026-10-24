package hosts

import (
	"errors"
	"io"
	"testing"

	"github.com/pkg/sftp"
)

// failingWrites lets the lock be created and chmodded but fails the nonce write.
type failingWrites struct{ sftp.FileWriter }

type failingWriterAt struct{}

func (failingWriterAt) WriteAt([]byte, int64) (int, error) { return 0, errors.New("disk full") }

func (f failingWrites) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	if _, err := f.FileWriter.Filewrite(r); err != nil {
		return nil, err
	}
	return failingWriterAt{}, nil
}

func memSFTP(t *testing.T, wrap func(*sftp.Handlers)) *sftp.Client {
	t.Helper()
	clientConn, serverConn := pipePair()
	handlers := sftp.InMemHandler()
	if wrap != nil {
		wrap(&handlers)
	}
	server := sftp.NewRequestServer(serverConn, handlers)
	go func() { _ = server.Serve() }()
	t.Cleanup(func() { _ = server.Close() })
	fs, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs
}

func TestNewInstallLock(t *testing.T) {
	fs := memSFTP(t, nil)
	nonce, err := newInstallLock(fs, "/lock")
	if err != nil || nonce == "" {
		t.Fatalf("first lock: nonce=%q err=%v", nonce, err)
	}
}

// A failed nonce write must fail the lock and remove the half-written file;
// it used to be dropped silently, leaving an empty lock that looked valid.
func TestNewInstallLockWriteFailureRemovesLock(t *testing.T) {
	fs := memSFTP(t, func(h *sftp.Handlers) { h.FilePut = failingWrites{h.FilePut} })
	nonce, err := newInstallLock(fs, "/lock")
	if err == nil || nonce != "" {
		t.Fatalf("nonce=%q err=%v; want a write error", nonce, err)
	}
	if _, statErr := fs.Stat("/lock"); statErr == nil {
		t.Fatal("failed lock was left behind")
	}
}

package config

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestStateLockExcludesAnotherWriterAndReleasesOnClose(t *testing.T) {
	root := t.TempDir()
	first, err := LockStateDir(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := LockStateDir(root)
	if err == nil {
		_ = second.Close()
		t.Fatal("expected a second writer to be rejected")
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("expected an in-use lock, observed %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := LockStateDir(root)
	if err != nil {
		t.Fatalf("expected lock release on close, observed %v", err)
	}
	_ = third.Close()
}

func TestStateLockRejectsUnsafeFilesWithoutChangingThem(t *testing.T) {
	for _, kind := range []string{"public", "symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "sentinel")
			lockPath := filepath.Join(root, "workbench.lock")
			const original = "KEEP_NEW_STATE_SENTINEL"
			if err := os.WriteFile(target, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "public":
				err = os.WriteFile(lockPath, []byte(original), 0644)
				if err == nil {
					err = os.Chmod(lockPath, 0644)
				}
			case "symlink":
				err = os.Symlink(target, lockPath)
			case "hardlink":
				err = os.Link(target, lockPath)
			}
			if err != nil {
				t.Fatal(err)
			}
			if lock, err := LockStateDir(root); err == nil {
				_ = lock.Close()
				t.Fatalf("expected %s lock to be rejected", kind)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != original {
				t.Fatalf("sentinel changed: read error=%v", err)
			}
			info, err := os.Stat(target)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("sentinel permissions changed: stat error=%v", err)
			}
		})
	}
}

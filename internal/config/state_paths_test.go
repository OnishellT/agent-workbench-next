package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRejectsSymlinkedStateDir(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.Chmod(target, 0750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "state-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load([]string{"--state-dir", link}); err == nil {
		t.Fatal("symlinked state directory accepted")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0750 {
		t.Fatalf("symlink target permissions changed: %04o", info.Mode().Perm())
	}
}

func TestLoadRejectsSymlinkedTokenFile(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "token")
	if err := os.WriteFile(outside, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(state, "access.token")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load([]string{"--state-dir", state}); err == nil {
		t.Fatal("symlinked token file accepted")
	}
	content, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "unchanged" {
		t.Fatalf("outside token target changed: %q", content)
	}
}

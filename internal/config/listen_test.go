package config

import (
	"path/filepath"
	"testing"
)

func TestLoadRejectsHTTPPublicURLOnWrongPort(t *testing.T) {
	_, err := Load([]string{
		"--state-dir", filepath.Join(t.TempDir(), "state"),
		"--listen", "127.0.0.1:18888",
		"--public-url", "http://localhost:19999",
	})
	if err == nil {
		t.Fatal("local public URL with a different listener port was accepted")
	}
}

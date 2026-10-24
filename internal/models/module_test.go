package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewFailsClearlyWhenNativeMagpieBinaryIsMissing(t *testing.T) {
	t.Setenv("WORKBENCH_MAGPIE_BIN", filepath.Join(t.TempDir(), "missing-magpie"))
	root := t.TempDir()
	_, err := New(filepath.Join(root, "state"), "http://127.0.0.1", "127.0.0.1:18888", filepath.Join(root, "agent-home"))
	if err == nil || err.Error() != "native Magpie binary is missing or not executable" {
		t.Fatalf("New error = %v, want a clear missing native binary error", err)
	}
	if _, err := os.Stat(filepath.Join(root, "agent-home")); !os.IsNotExist(err) {
		t.Fatalf("private agent home was prepared before binary validation: %v", err)
	}
}

func TestRemoteOnlyModuleServesUnavailableWithoutFallback(t *testing.T) {
	m := NewRemoteOnly()
	if m == nil {
		t.Fatal("NewRemoteOnly returned nil")
	}
	if err := m.Probe("codex", ""); err == nil {
		t.Fatal("remote-only Probe succeeded without a native actor")
	}
	if m.SiteAddr() != "" || m.SiteOrigin() != "" {
		t.Fatalf("remote-only site = %q %q, want empty", m.SiteAddr(), m.SiteOrigin())
	}
	for _, handler := range []http.Handler{m.Handler(), m.AgentHandler(), m.SiteHandler()} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("remote-only handler = %d, want 503", response.Code)
		}
	}
	if diagnostics := m.Diagnostics(context.Background()); !strings.Contains(string(diagnostics), `"state":"unavailable"`) {
		t.Fatalf("remote-only diagnostics do not report unavailable: %s", diagnostics)
	}
	if err := m.Close(context.Background()); err != nil {
		t.Fatalf("remote-only Close = %v, want nil", err)
	}
}

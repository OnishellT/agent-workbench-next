package models

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestAttachExistingMagpieKeepsStateAuthenticationAndLifecycle(t *testing.T) {
	const key = "existing-magpie-private-key-0123456789"
	port := ""
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("magpie_web_" + port)
		if err != nil || cookie.Value != key || r.Header.Get("Origin") != "http://"+r.Host {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/state" {
			_, _ = w.Write([]byte(`{"agents":[{"id":"codex","wired":true}]}`))
			return
		}
		_, _ = w.Write([]byte("magpie"))
	}))
	defer web.Close()
	u, _ := url.Parse(web.URL)
	port = u.Port()
	root := t.TempDir()
	path := filepath.Join(root, "attach.json")
	writeConfig := func(secret string, mode os.FileMode) {
		t.Helper()
		data, _ := json.Marshal(map[string]string{"web_url": web.URL, "gateway_url": web.URL, "web_key": secret})
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(key, 0o600)
	t.Setenv("WORKBENCH_MAGPIE_ATTACH_FILE", path)
	t.Setenv("WORKBENCH_MAGPIE_BIN", "/nonexistent/no-second-magpie")
	m, err := New(filepath.Join(root, "state"), "http://127.0.0.1:18888", "127.0.0.1:18888", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Probe("codex", ""); err != nil {
		t.Fatalf("lost existing connected agent: %v", err)
	}
	if !m.native.diagnostics(context.Background()).Healthy {
		t.Fatal("attached Magpie not reported healthy")
	}
	if err := m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Probe("codex", ""); err != nil {
		t.Fatalf("closing Workbench stopped external Magpie: %v", err)
	}
	writeConfig("wrong-private-magpie-key-0123456789", 0o600)
	if _, err := attachNativeProcess(path, "http://127.0.0.1:18888", 19000); err == nil {
		t.Fatal("accepted wrong web credential")
	}
	writeConfig(key, 0o644)
	if _, err := attachNativeProcess(path, "http://127.0.0.1:18888", 19000); err == nil {
		t.Fatal("accepted publicly readable credential file")
	}
}

func TestAttachedMagpieRejectsNonLoopbackAndNonOriginURLs(t *testing.T) {
	for _, value := range []string{"http://example.com:80", "http://10.0.0.50:3425", "http://localhost:3425", "http://127.0.0.1", "http://127.0.0.1:3425/api/state", "http://user@127.0.0.1:3425", "http://127.0.0.1:3425?key=secret", "http://127.0.0.1:3425#fragment", "https://127.0.0.1:3425"} {
		if _, _, err := attachedLoopbackURL(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

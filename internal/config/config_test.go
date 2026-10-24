package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCreatesPrivateStateAndReusesToken(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "fresh state")
	args := []string{"--state-dir", state, "--listen", "[::1]:18890", "--public-url", "https://workbench.example:8443", "--trusted-proxy", "127.0.0.1"}
	first, err := Load(args)
	if err != nil {
		t.Fatal(err)
	}
	if first.StateDir != state || first.Listen != "[::1]:18890" || first.PublicURL != "https://workbench.example:8443" {
		t.Fatalf("resolved config = %#v", first)
	}
	if len(first.TrustedProxies) != 1 || first.TrustedProxies[0].String() != "127.0.0.1/32" {
		t.Fatalf("trusted proxies = %v", first.TrustedProxies)
	}
	stateInfo, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if stateInfo.Mode().Perm() != 0700 {
		t.Fatalf("state permissions = %04o, want 0700", stateInfo.Mode().Perm())
	}
	secretInfo, err := os.Stat(filepath.Join(state, "access.token"))
	if err != nil {
		t.Fatal(err)
	}
	if secretInfo.Mode().Perm() != 0600 {
		t.Fatalf("token permissions = %04o, want 0600", secretInfo.Mode().Perm())
	}
	second, err := Load(args)
	if err != nil {
		t.Fatal(err)
	}
	if second.AuthToken != first.AuthToken {
		t.Fatal("existing state token was rotated")
	}
}

func TestLoadUsesFreshXDGNamespaceWithoutLegacyStateDiscovery(t *testing.T) {
	root := t.TempDir()
	xdg := filepath.Join(root, "xdg")
	home := filepath.Join(root, "home")
	legacy := filepath.Join(home, ".local", "state", "agent-workbench")
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "access.token"), []byte(strings.Repeat("L", 48)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", xdg)
	t.Setenv("HOME", home)
	t.Setenv("WORKBENCH_STATE", legacy)

	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(xdg, stateName)
	if cfg.StateDir != want {
		t.Fatalf("state dir = %q, want isolated %q", cfg.StateDir, want)
	}
	if cfg.AuthToken == strings.Repeat("L", 48) {
		t.Fatal("legacy token was imported")
	}
	if _, err := os.Stat(filepath.Join(want, "access.token")); err != nil {
		t.Fatalf("new-state token not created: %v", err)
	}
}

func TestLoadRejectsRelativeStateAndExternalTokenFile(t *testing.T) {
	if _, err := Load([]string{"--state-dir", "relative/state"}); err == nil {
		t.Fatal("relative state dir accepted")
	}
	root := t.TempDir()
	if _, err := Load([]string{"--state-dir", filepath.Join(root, "state"), "--token-file", filepath.Join(root, "elsewhere", "token")}); err == nil {
		t.Fatal("token file outside private state accepted")
	}
}

func TestLoadDoesNotRepairOrRotateInsecureExistingToken(t *testing.T) {
	state := t.TempDir()
	secret := filepath.Join(state, "access.token")
	if err := os.WriteFile(secret, []byte(strings.Repeat("S", 48)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secret, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load([]string{"--state-dir", state}); err == nil {
		t.Fatal("world-readable token was accepted")
	}
	info, err := os.Stat(secret)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("insecure token permissions were silently changed: %04o", info.Mode().Perm())
	}
}

func TestNormalizePublicURL(t *testing.T) {
	valid := map[string]string{
		"HTTPS://Example.COM:443/": "https://example.com",
		"http://[::1]:18888":       "http://[::1]:18888",
		"http://localhost:8080":    "http://localhost:8080",
	}
	for input, want := range valid {
		got, err := NormalizePublicURL(input)
		if err != nil || got != want {
			t.Errorf("NormalizePublicURL(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	invalid := []string{
		"http://example.com",
		"https://user:pass@example.com",
		"https://example.com/path",
		"https://example.com/?q=x",
		"https://example.com#fragment",
		"https://example.com.",
		"https://example.com:65536",
		"https://bad host",
	}
	for _, input := range invalid {
		if got, err := NormalizePublicURL(input); err == nil {
			t.Errorf("NormalizePublicURL(%q) = %q, want error", input, got)
		}
	}
}

func TestLoadRejectsUnsafeListenAndUntrustedProxyInput(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"--state-dir", filepath.Join(root, "state1"), "--listen", "0.0.0.0:18888"},
		{"--state-dir", filepath.Join(root, "state2"), "--listen", "127.0.0.1:80"},
		{"--state-dir", filepath.Join(root, "state3"), "--trusted-proxy", "not-an-ip"},
	} {
		if _, err := Load(args); err == nil {
			t.Errorf("Load(%v) succeeded, want validation error", args)
		}
	}
}

func TestLoadDoesNotGenerateTokenForNonemptyStateWithoutToken(t *testing.T) {
	state := t.TempDir()
	marker := filepath.Join(state, "settings.json")
	if err := os.WriteFile(marker, []byte(`{"existing":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load([]string{"--state-dir", state}); err == nil {
		t.Fatal("generated a new access token for nonempty existing state")
	}
	if _, err := os.Stat(filepath.Join(state, "access.token")); !os.IsNotExist(err) {
		t.Fatalf("token file exists after refused state adoption: %v", err)
	}
}

func TestExplicitStateDirWorksWithoutHomeOrXDG(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	state := filepath.Join(t.TempDir(), "absolute state")
	cfg, err := Load([]string{"--state-dir", state})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateDir != state {
		t.Fatalf("state dir = %q, want %q", cfg.StateDir, state)
	}
}

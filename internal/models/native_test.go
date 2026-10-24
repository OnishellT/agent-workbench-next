package models

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNativePortsAreUniqueLoopbackAndPersisted(t *testing.T) {
	root := t.TempDir()
	if err := privateDir(root); err != nil {
		t.Fatal(err)
	}
	first, err := loadNativePorts(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Site == first.Web || first.Site == first.Gateway || first.Web == first.Gateway {
		t.Fatalf("native ports are not distinct: %+v", first)
	}
	for _, port := range []int{first.Site, first.Web, first.Gateway} {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			t.Fatalf("port %d is not a free loopback port: %v", port, err)
		}
		_ = listener.Close()
	}
	reloaded, err := loadNativePorts(root)
	if err != nil || reloaded != first {
		t.Fatalf("persisted ports = %+v, %v; want %+v", reloaded, err, first)
	}
	info, err := os.Stat(filepath.Join(root, "native-ports.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("native port state permissions = %v, %v", info, err)
	}
	otherRoot := t.TempDir()
	if err := privateDir(otherRoot); err != nil {
		t.Fatal(err)
	}
	other, err := loadNativePorts(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Fatalf("independent native instances reused the same ports: %+v", first)
	}
}

func TestNativeChildEnvironmentIsPrivateAndSeparatesGatewayFromWebOrigin(t *testing.T) {
	t.Setenv("PATH", "/fixture/bin")
	t.Setenv("MAGPIE_BUN", "/fixture/bin/bun")
	for _, name := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "PI_CODING_AGENT_DIR", "SSH_AUTH_SOCK", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "MAGPIE_ADDR", "MAGPIE_PUBLIC_URL", "MAGPIE_WEB_KEY"} {
		t.Setenv(name, "operator-poison-must-not-inherit")
	}
	got := map[string]string{}
	for _, entry := range nativeEnvironment("/private/workbench/home", "/private/state", "https://workbench.example/ui", "127.0.0.1:23456", "fixture-web-key") {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("invalid environment entry %q", entry)
		}
		got[name] = value
	}
	want := map[string]string{
		"HOME": "/private/workbench/home", "XDG_CONFIG_HOME": "/private/workbench/home/.config",
		"XDG_CACHE_HOME": "/private/workbench/home/.cache", "XDG_DATA_HOME": "/private/workbench/home/.local/share",
		"PATH": "/fixture/bin", "MAGPIE_ADDR": "127.0.0.1:23456",
		"MAGPIE_BUN":        "/fixture/bin/bun",
		"MAGPIE_PUBLIC_URL": "http://127.0.0.1:23456", "MAGPIE_WEB_URL": "https://workbench.example/ui",
		"MAGPIE_WEB_KEY": "fixture-web-key",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("child env %s = %q, want %q", key, got[key], value)
		}
	}
	for _, name := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "PI_CODING_AGENT_DIR", "SSH_AUTH_SOCK", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		if _, exists := got[name]; exists {
			t.Errorf("operator environment variable %s leaked to native child", name)
		}
	}
	if _, err := strconv.Atoi(strings.TrimPrefix(got["MAGPIE_ADDR"], "127.0.0.1:")); err != nil {
		t.Fatalf("invalid private gateway address: %v", err)
	}
}

func TestPrepareNativeHomeKeepsRealHomeUntouched(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// A pre-existing user config dir with user permissions and content.
	existing := filepath.Join(home, ".config")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existing, "user.conf")
	if err := os.WriteFile(marker, []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	if err := prepareNativeHome(home, state); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(home); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("real HOME permissions changed: %v %v", info, err)
	}
	if info, err := os.Stat(existing); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("existing user dir permissions changed: %v %v", info, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("existing user file lost: %v", err)
	}
	for _, dir := range []string{
		filepath.Join(home, ".cache"),
		filepath.Join(home, ".local", "share"),
		filepath.Join(home, ".local", "state"),
	} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("missing native subdir %s: %v", dir, err)
		}
	}
	if info, err := os.Stat(filepath.Join(state, "run")); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("private run dir not under state root: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(home), "run")); !os.IsNotExist(err) {
		t.Fatalf("run dir leaked beside the real HOME")
	}
}

func TestPrepareNativeHomeRejectsNonRealHome(t *testing.T) {
	state := t.TempDir()
	if err := prepareNativeHome(filepath.Join(t.TempDir(), "missing"), state); err == nil {
		t.Fatal("missing HOME accepted")
	}
	linkFarm := t.TempDir()
	link := filepath.Join(linkFarm, "home-link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if err := prepareNativeHome(link, state); err == nil {
		t.Fatal("symlinked HOME accepted")
	}
	file := filepath.Join(linkFarm, "plain-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareNativeHome(file, state); err == nil {
		t.Fatal("non-directory HOME accepted")
	}
	if err := prepareNativeHome("relative/home", state); err == nil {
		t.Fatal("relative HOME accepted")
	}
}

func TestNativeSiteOriginKeepsConfiguredTLSOriginAndUsesLocalPortByDefault(t *testing.T) {
	t.Setenv("WORKBENCH_MAGPIE_ORIGIN", "")
	for _, test := range []struct{ public, want string }{
		{"http://127.0.0.1:18921", "http://127.0.0.1:39010"},
	} {
		got, err := siteOrigin(test.public, 39010)
		if err != nil || got != test.want {
			t.Errorf("siteOrigin(%q) = %q, %v; want %q", test.public, got, err, test.want)
		}
	}
	for _, public := range []string{"https://127.0.0.1:18904", "https://workbench.example.com"} {
		if _, err := siteOrigin(public, 39010); err == nil {
			t.Errorf("HTTPS Workbench origin %q accepted without a distinct Magpie origin", public)
		}
	}
	t.Setenv("WORKBENCH_MAGPIE_ORIGIN", "https://magpie.example:443")
	got, err := siteOrigin("http://127.0.0.1:18921", 39010)
	if err != nil || got != "https://magpie.example" {
		t.Fatalf("configured public native origin = %q, %v", got, err)
	}
	if _, err := siteOrigin("https://magpie.example", 39010); err == nil {
		t.Fatal("Magpie accepted the same canonical HTTPS origin as Workbench")
	}
	t.Setenv("WORKBENCH_MAGPIE_ORIGIN", "http://127.0.0.1:18921")
	if _, err := siteOrigin("http://127.0.0.1:18921", 39010); err == nil {
		t.Fatal("Magpie accepted the same HTTP origin as Workbench")
	}
	t.Setenv("WORKBENCH_MAGPIE_ORIGIN", "http://magpie.example")
	if _, err := siteOrigin("http://127.0.0.1:18921", 39010); err == nil {
		t.Fatal("non-loopback HTTP native origin accepted")
	}
}

func TestNativeXDGsPreserveOperatorOverridesOnlyForOwnHome(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(home, "custom-cfg")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", custom)
	t.Setenv("XDG_CACHE_HOME", "relative-ignored")
	t.Setenv("XDG_DATA_HOME", "")
	customState := filepath.Join(home, "custom-state")
	t.Setenv("XDG_STATE_HOME", customState)
	state := t.TempDir()
	envOf := func(entries []string) map[string]string {
		got := map[string]string{}
		for _, entry := range entries {
			name, value, ok := strings.Cut(entry, "=")
			if !ok {
				t.Fatalf("invalid environment entry %q", entry)
			}
			got[name] = value
		}
		return got
	}
	own := envOf(nativeEnvironment(home, state, "https://workbench.example/ui", "127.0.0.1:23456", "fixture-web-key"))
	if own["XDG_CONFIG_HOME"] != custom {
		t.Fatalf("own XDG_CONFIG_HOME = %q, want preserved %q", own["XDG_CONFIG_HOME"], custom)
	}
	if own["XDG_STATE_HOME"] != customState {
		t.Fatalf("own XDG_STATE_HOME = %q, want preserved %q", own["XDG_STATE_HOME"], customState)
	}
	for name, want := range map[string]string{
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"XDG_RUNTIME_DIR": filepath.Join(state, "run"),
	} {
		if own[name] != want {
			t.Errorf("own %s = %q, want %q", name, own[name], want)
		}
	}
	foreign := envOf(nativeEnvironment("/foreign/home", state, "https://workbench.example/ui", "127.0.0.1:23456", "fixture-web-key"))
	for name, want := range map[string]string{
		"XDG_CONFIG_HOME": "/foreign/home/.config",
		"XDG_CACHE_HOME":  "/foreign/home/.cache",
		"XDG_DATA_HOME":   "/foreign/home/.local/share",
		"XDG_STATE_HOME":  "/foreign/home/.local/state",
		"XDG_RUNTIME_DIR": filepath.Join(state, "run"),
	} {
		if foreign[name] != want {
			t.Errorf("foreign %s = %q, want %q (operator env must not leak)", name, foreign[name], want)
		}
	}
	if err := prepareNativeHome(home, state); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(custom); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("inherited XDG dir not prepared: %v %v", info, err)
	}
}

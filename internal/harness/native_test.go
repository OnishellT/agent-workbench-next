package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCommandPreservesUserProfileAndDoesNotPreparePrivateHome(t *testing.T) {
	e, root, bin := testEngines(t, nil)
	writeStub(t, filepath.Join(bin, "codex"))
	writeStub(t, filepath.Join(bin, "omp"))
	e.router = nil // Native authentication is owned by the agent; no gateway rewiring.
	e.env = append(e.env, "CODEX_HOME="+filepath.Join(root, "existing-codex"), "XDG_CONFIG_HOME="+filepath.Join(root, "config"))
	for _, engine := range []string{EngineCodex, EngineOMP} {
		c, err := e.NativeCommand(Spec{Engine: engine, Permission: PermissionWorkspaceWrite, SessionDir: root})
		if err != nil {
			t.Fatal(err)
		}
		env := envMap(c.Env)
		if env["HOME"] != filepath.Join(root, "user-home") || env["CODEX_HOME"] != filepath.Join(root, "existing-codex") || env["XDG_CONFIG_HOME"] != filepath.Join(root, "config") {
			t.Fatalf("native profile changed: %s", engine)
		}
		if strings.Contains(strings.Join(c.Args, " "), " -m ") {
			t.Fatal("model override")
		}
	}
	if _, err := os.Stat(e.AgentHome()); !os.IsNotExist(err) {
		t.Fatalf("private HOME created: %v", err)
	}
	if _, err := e.NativeCommand(Spec{Engine: EngineCodex, Model: "other", Permission: PermissionWorkspaceWrite}); err == nil {
		t.Fatal("model override accepted")
	}
}

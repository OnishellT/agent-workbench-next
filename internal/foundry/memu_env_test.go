package foundry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"workbench/internal/hosts"
)

func TestMemuChildEnvironmentIsAllowlisted(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.env")
	if err := os.WriteFile(config, []byte("MEMU_DB=/from/file.sqlite\nMEMU_ONLY_FILE=1\nOTHER_SECRET=nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMU_CONFIG_ENV", config)
	t.Setenv("MEMU_DB", "/from/env.sqlite")
	t.Setenv("WORKBENCH_SESSION_CANARY", "canary-session")
	t.Setenv("OPENAI_API_KEY", "canary-key")
	t.Setenv("LC_ALL", "C.UTF-8")
	env := strings.Join(memuChildEnvironment(), "\n")
	for _, leaked := range []string{"canary-session", "canary-key", "OTHER_SECRET", "nope"} {
		if strings.Contains(env, leaked) {
			t.Fatalf("%q leaked into the memU environment:\n%s", leaked, env)
		}
	}
	for _, want := range []string{"PATH=", "MEMU_DB=/from/env.sqlite", "MEMU_ONLY_FILE=1", "MEMU_CONFIG_ENV=" + config, "LC_ALL=C.UTF-8"} {
		if !strings.Contains(env, want) {
			t.Fatalf("missing %q in the memU environment:\n%s", want, env)
		}
	}
}

// A host session whose identity cannot be confirmed is rejected before any of its
// content is written to the export directory; a disabled source is never exported.
func TestMemuExportChecksIdentityBeforeWritingAndSkipsDisabledSources(t *testing.T) {
	root := t.TempDir()
	module, server := newFoundryTestServer(t, filepath.Join(root, "state"), &historyHostScope{rows: map[string]hosts.Host{}})
	enabled := captureMemuFixture(t, module, server, filepath.Join(root, "a"), "export-a", "/workspace/a")
	disabled := captureMemuFixture(t, module, server, filepath.Join(root, "b"), "export-b", "/workspace/b")
	_ = enabled
	if _, err := module.db.Exec(`UPDATE sources SET enabled=0 WHERE id=?`, disabled); err != nil {
		t.Fatal(err)
	}
	exportDir := filepath.Join(root, "export")
	manifest, apiErr := module.exportMemuEvidence(t.Context(), exportDir)
	if apiErr != nil || len(manifest.Sessions) != 1 || manifest.Sessions[0].ID != "export-a" {
		t.Fatalf("manifest=%+v err=%+v", manifest, apiErr)
	}
	if _, err := module.db.Exec(`UPDATE sessions SET host_id='ghost-host' WHERE id='export-a'`); err != nil {
		t.Fatal(err)
	}
	exportDir = filepath.Join(root, "export2")
	if _, apiErr := module.exportMemuEvidence(t.Context(), exportDir); apiErr == nil || apiErr.Code != "host_scope_denied" {
		t.Fatalf("want host_scope_denied, got %+v", apiErr)
	}
	if files, _ := filepath.Glob(filepath.Join(exportDir, "*.jsonl")); len(files) != 0 {
		t.Fatalf("content written before the identity check: %v", files)
	}
}

func TestSafeLearningErrorRedactsBeforeTruncatingAndWithControlCharacters(t *testing.T) {
	secret := "sk-" + strings.Repeat("a1B2c3D4", 6)
	for name, input := range map[string]string{
		"plain":    "failed: api_key=" + secret + " trailing",
		"control":  "boom\x00\x01 api_key=" + secret,
		"toolong":  strings.Repeat("x", 3000) + " api_key=" + secret,
		"split":    "api_key=" + secret,
		"truncate": "prefix api_key=" + secret,
	} {
		for _, limit := range []int{12, 25, 40, 100} {
			got := safeLearningError(input, limit)
			if strings.Contains(got, secret) || strings.Contains(got, secret[:20]) {
				t.Fatalf("%s limit=%d leaked: %q", name, limit, got)
			}
		}
	}
}

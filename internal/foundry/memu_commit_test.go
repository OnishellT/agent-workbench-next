package foundry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemuIndexRejectsScriptsAndResources(t *testing.T) {
	m := &Module{}
	for _, target := range []string{".agents/skills/check/verification.json", "scripts/verify.py", "scripts/verify.sh"} {
		err := m.indexMemuAdoption(context.Background(), memuAdoptionRecord{Target: target}, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "Markdown") {
			t.Fatalf("non-Markdown index %q = %v", target, err)
		}
	}
}

func TestMemuIndexOnlyApprovedSnapshotAndRestoreAfterRevocation(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	m, server := newFoundryTestServer(t, state, nil)
	m.projectRoots = func() []string { return []string{project} }
	source := captureMemuFixture(t, m, server, filepath.Join(root, "evidence"), "index-evidence", project)
	base, err := m.memuBase()
	if err != nil {
		t.Fatal(err)
	}
	if _, apiErr := m.exportMemuEvidence(context.Background(), filepath.Join(base, "export")); apiErr != nil {
		t.Fatal(apiErr)
	}
	bin := filepath.Join(root, "bin")
	payload := filepath.Join(root, "payload.json")
	t.Setenv("WORKBENCH_MEMU_BIN", writeExec(t, filepath.Join(bin, "memu-codex"), "#!/bin/sh\nexit 0\n"))
	t.Setenv("MEMU_DB", filepath.Join(root, "memory.sqlite3"))
	t.Setenv("MEMU_MEMORY_MODE", "local")
	t.Setenv("MEMU_CONFIG_ENV", filepath.Join(root, "config.env"))
	t.Setenv("MEMU_TEST_PAYLOAD", payload)
	writeExec(t, filepath.Join(bin, "memu"), `#!/bin/sh
if [ "$1" = list-files ]; then printf '{"recall_files":[]}'; else cat > "$MEMU_TEST_PAYLOAD"; printf '{}'; fi
`)
	dir := filepath.Join(base, "adoptions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	content := []byte("# Approved domain skill\nDo one verified thing.\n")
	target := filepath.Join(project, "AGENTS.md")
	final := append([]byte("existing private rule\n"), content...)
	if err := os.WriteFile(target, final, 0644); err != nil {
		t.Fatal(err)
	}
	id := "0011223344556677"
	sourceHash := sha256.Sum256(content)
	targetHash := sha256.Sum256(final)
	record := memuAdoptionRecord{ID: id, Project: project, Target: "AGENTS.md", SourceSHA256: hex.EncodeToString(sourceHash[:]), AfterSHA256: hex.EncodeToString(targetHash[:])}
	saveRecord := func() {
		t.Helper()
		raw, _ := json.Marshal(record)
		if err := os.WriteFile(filepath.Join(dir, id+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	saveRecord()
	if err := os.WriteFile(filepath.Join(dir, id+".approved"), content, 0600); err != nil {
		t.Fatal(err)
	}
	evidence, err := os.ReadFile(filepath.Join(base, "export", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".evidence"), evidence, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.indexMemuAdoption(context.Background(), record, dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "existing private rule") || !strings.Contains(string(raw), "Approved domain skill") {
		t.Fatal("index did not contain exactly the approved proposal")
	}
	if _, err := m.db.Exec("UPDATE sources SET enabled=0 WHERE id=?", source); err != nil {
		t.Fatal(err)
	}
	if err := m.indexMemuAdoption(context.Background(), record, dir); err == nil {
		t.Fatal("revoked evidence was indexed")
	}
	record.RolledBack = true
	saveRecord()
	if err := m.RestoreMemuIndex(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Approved domain skill") || !strings.Contains(string(raw), "withdrawn") {
		t.Fatal("withdrawn memory still contains the proposal")
	}
}

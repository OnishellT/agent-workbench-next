// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The app's own Work transcripts are captured through the source this registers: one
// canonical source for that directory, no scan and no other root, and a moved state root
// follows the new path without losing the source identity or its pause.
func TestEnsureWorkSourceRegistersOnlyTheWorkDirectoryAndFollowsIt(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	module, server := newFoundryTestServer(t, stateDir, nil)
	defer server.Close()
	dir := filepath.Join(stateDir, "conversations")
	if err := module.EnsureWorkSource(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if err := module.EnsureWorkSource(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	var id, path, adapter string
	var enabled int
	if err := module.db.QueryRow(`SELECT id,path,adapter,enabled FROM sources`).Scan(&id, &path, &adapter, &enabled); err != nil {
		t.Fatal(err)
	}
	if id != workSourceID || path != dir || adapter != string(AdapterCanonical) || enabled != 1 {
		t.Fatalf("work source = %q %q %q enabled=%d", id, path, adapter, enabled)
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("work source directory = %v %v", info, err)
	}
	var jobs, sessions int
	if err := module.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := module.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 || sessions != 0 {
		t.Fatalf("registering the work source scanned or captured on its own: jobs=%d sessions=%d", jobs, sessions)
	}

	// The operator pauses the own source: a new start keeps that decision.
	if _, err := module.db.Exec(`UPDATE sources SET enabled=0 WHERE id=?`, workSourceID); err != nil {
		t.Fatal(err)
	}
	if err := module.EnsureWorkSource(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if err := module.db.QueryRow(`SELECT enabled FROM sources WHERE id=?`, workSourceID).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 {
		t.Fatal("re-registering the work source reactivated a paused source")
	}

	// A moved state root relinks the same source instead of registering a second one.
	moved := filepath.Join(t.TempDir(), "state-α with spaces", "conversations")
	if err := module.EnsureWorkSource(context.Background(), moved); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := module.db.QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("moved state registered %d sources", count)
	}
	if err := module.db.QueryRow(`SELECT id,path FROM sources`).Scan(&id, &path); err != nil {
		t.Fatal(err)
	}
	if id != workSourceID || path != moved {
		t.Fatalf("relinked work source = %q %q, want %q %q", id, path, workSourceID, moved)
	}
}

// A registration the operator made for the same directory is respected: the shell does not
// add a second source over it.
func TestEnsureWorkSourceKeepsAnExistingRegistrationOfTheSamePath(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	module, server := newFoundryTestServer(t, stateDir, nil)
	defer server.Close()
	dir := filepath.Join(stateDir, "conversations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	status, data := callFoundryJSON(t, client, server.URL, "POST", "/api/conversations/sources", map[string]any{"path": dir, "adapter": "canonical"})
	if status != 201 {
		t.Fatalf("register explicit source status=%d body=%+v", status, data)
	}
	registered := foundryObject(t, data["source"])["id"].(string)
	if err := module.EnsureWorkSource(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := module.db.QueryRow(`SELECT id FROM sources`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != registered {
		t.Fatalf("work source overrode the operator registration: %q", id)
	}
}

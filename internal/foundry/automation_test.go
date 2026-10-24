// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutomaticLocalCaptureUsesOnlyEnabledLocalSources(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sourceDir := filepath.Join(t.TempDir(), "agent-history")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceDir, "first.jsonl"), map[string]any{
		"session_id": "auto-local-session",
		"role":       "user",
		"kind":       "message",
		"text":       "capturada sin pulsar Sincronizar",
	})

	module, server := newFoundryTestServer(t, stateDir, nil)
	if _, err := module.db.Exec(`UPDATE task_settings SET poll_seconds=3600 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{
		"path": sourceDir, "adapter": "canonical",
	})
	if status != http.StatusCreated {
		t.Fatalf("create source: %d %v", status, body)
	}
	source := foundryObject(t, body["source"])
	sourceID, _ := source["id"].(string)
	if sourceID == "" {
		t.Fatal("source id is empty")
	}
	module.notifyCaptureScheduler()

	var session map[string]any
	waitFor(t, 3*time.Second, func() bool {
		status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/conversations", nil)
		if status != http.StatusOK {
			return false
		}
		rows, _ := body["sessions"].([]any)
		if len(rows) != 1 {
			return false
		}
		session, _ = rows[0].(map[string]any)
		return session != nil
	})
	if session["id"] != "auto-local-session" || session["source_id"] != sourceID || session["host_id"] != "local" {
		t.Fatalf("automatic capture signature = %v", session)
	}
	t.Logf("automatic capture signature: session=%v source=%v host=%v adapter=%v", session["id"], session["source_id"], session["host_id"], session["adapter"])
	waitFor(t, 2*time.Second, func() bool {
		var scans int
		if err := module.db.QueryRow(`SELECT COUNT(*) FROM sources WHERE id=? AND last_scan_at IS NOT NULL`, sourceID).Scan(&scans); err != nil {
			t.Fatal(err)
		}
		return scans == 1
	})
	var jobs int
	if err := module.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("automatic capture created %d visible scan jobs", jobs)
	}

	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/conversations/sources/"+sourceID, map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("pause source: %d %v", status, body)
	}
	writeJSONLFixture(t, filepath.Join(sourceDir, "second.jsonl"), map[string]any{
		"session_id": "paused-source-session",
		"role":       "user",
		"kind":       "message",
		"text":       "una fuente pausada no debe capturarse",
	})

	remoteDir := filepath.Join(t.TempDir(), "remote-history")
	if err := os.MkdirAll(remoteDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(remoteDir, "remote.jsonl"), map[string]any{
		"session_id": "remote-source-session",
		"role":       "user",
		"kind":       "message",
		"text":       "host-history no se enumera como archivo local",
	})
	if _, err := module.db.Exec(`INSERT INTO sources(id,path,adapter,enabled,created,coverage_json) VALUES('host-history-test',?,'canonical',1,?,?)`, remoteDir, nowSeconds(), `{"status":"unknown"}`); err != nil {
		t.Fatal(err)
	}

	// captureLocalSources holds captureMu, so when it returns any pass already running
	// (the scheduler's included) has finished: no fixed sleep needed.
	module.captureLocalSources(t.Context())
	var sessions int
	if err := module.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 {
		t.Fatalf("paused or remote source was captured automatically: %d sessions", sessions)
	}
}

func TestAutomaticLocalCaptureStopsWithModuleClose(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "agent-history")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceDir, "held.jsonl"), map[string]any{
		"session_id": "auto-close-session",
		"role":       "user",
		"kind":       "message",
		"text":       "la captura automática debe cancelarse",
		"host_id":    "auto-close-host",
	})
	hostScope := &blockingHostScope{started: make(chan struct{})}
	module, server := newFoundryTestServer(t, filepath.Join(root, "state"), hostScope)
	defer server.Close()
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{
		"path": sourceDir, "adapter": "canonical",
	})
	if status != http.StatusCreated {
		t.Fatalf("create source: %d %v", status, body)
	}
	module.notifyCaptureScheduler()
	select {
	case <-hostScope.started:
	case <-time.After(3 * time.Second):
		t.Fatal("automatic capture never reached the host-scope cancellation gate")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := module.Close(ctx); err != nil {
		t.Fatalf("close did not cancel automatic capture: %v", err)
	}
	var sessions int
	if err := module.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err == nil {
		t.Fatal("module database stayed open after close")
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

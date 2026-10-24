// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSameSourceRelinkPreservesNativeIdentityRevisionTasksAndScanReceipt(t *testing.T) {
	root := t.TempDir()
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	firstRoot := filepath.Join(root, "first-source")
	if err := os.MkdirAll(firstRoot, 0700); err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(firstRoot, "stable.jsonl")
	writeJSONLFixture(t, firstPath, map[string]any{"session_id": "native-stable-session", "role": "user", "kind": "message", "text": "Keep the original evidence intact."}, map[string]any{"session_id": "native-stable-session", "role": "assistant", "kind": "message", "text": "The first captured revision."})
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": firstRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)
	queueFoundryScan(t, server.Client(), server.URL, sourceID)
	firstSessions, firstRevision := sourceConversations(t, server, sourceID)
	if len(firstSessions) != 1 {
		t.Fatalf("first scan expected one conversation, got %+v", firstSessions)
	}
	firstID := foundryObject(t, firstSessions[0])["id"].(string)
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/tasks", map[string]any{"session_id": firstID, "revision": firstRevision, "first_event": 0, "last_event": 1, "title": "Preserve source-bound task", "citations": []any{map[string]any{"event_index": 0, "quote": "Keep the original evidence intact"}}})
	if status != http.StatusCreated {
		t.Fatalf("create task status=%d body=%+v", status, body)
	}
	taskID := foundryObject(t, body["task"])["id"].(string)
	firstSource := loadSourceHTTP(t, server, sourceID)
	firstLastScan := foundryObject(t, firstSource["last_scan"])
	firstTimestamp, ok := firstSource["last_scan_at"].(float64)
	if !ok || firstLastScan["status"] != "complete" {
		t.Fatalf("scan receipt missing after successful scan: %+v", firstSource)
	}
	secondRoot := filepath.Join(root, "second-source")
	if err := os.MkdirAll(secondRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(secondRoot, "stable.jsonl"), map[string]any{"session_id": "native-stable-session", "role": "user", "kind": "message", "text": "Keep the original evidence intact."}, map[string]any{"session_id": "native-stable-session", "role": "assistant", "kind": "message", "text": "A genuinely new captured revision."})
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/conversations/sources/"+sourceID, map[string]any{"path": secondRoot})
	if status != http.StatusOK {
		t.Fatalf("relink source status=%d body=%+v", status, body)
	}
	patched := foundryObject(t, body["source"])
	if patched["path"] != secondRoot {
		t.Fatalf("source path not updated: %+v", patched)
	}
	patchedLast := foundryObject(t, patched["last_scan"])
	if patchedLast["status"] != "complete" || patched["last_scan_at"] != firstTimestamp || patchedLast["files"] != firstLastScan["files"] {
		t.Fatalf("relink reset the historical scan receipt: before=%+v after=%+v", firstSource, patched)
	}
	queueFoundryScan(t, server.Client(), server.URL, sourceID)
	secondSessions, secondRevision := sourceConversations(t, server, sourceID)
	if len(secondSessions) != 1 {
		t.Fatalf("relinked source duplicated conversation: %+v", secondSessions)
	}
	secondID := foundryObject(t, secondSessions[0])["id"].(string)
	if secondID != firstID {
		t.Fatalf("native session identity changed after path relink: first=%q second=%q", firstID, secondID)
	}
	if secondRevision == firstRevision {
		t.Fatalf("new content did not create a revision: %q", secondRevision)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/conversations/"+firstID+"/revisions", nil)
	if status != http.StatusOK || len(foundryList(t, body["revisions"])) != 2 {
		t.Fatalf("same native conversation should retain both immutable revisions: status=%d body=%+v", status, body)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/tasks/"+taskID, nil)
	if status != http.StatusOK || body["id"] != taskID {
		t.Fatalf("original task was not retained across revision: status=%d body=%+v", status, body)
	}
	thirdRoot := filepath.Join(root, "other-source")
	if err := os.MkdirAll(thirdRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(thirdRoot, "stable.jsonl"), map[string]any{"session_id": "native-stable-session", "role": "user", "kind": "message", "text": "Keep this other source isolated."})
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": thirdRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register second source status=%d body=%+v", status, body)
	}
	otherSource := foundryObject(t, body["source"])["id"].(string)
	queueFoundryScan(t, server.Client(), server.URL, otherSource)
	otherSessions, _ := sourceConversations(t, server, otherSource)
	if len(otherSessions) != 1 {
		t.Fatalf("second source expected a separately scoped conversation: %+v", otherSessions)
	}
	otherID := foundryObject(t, otherSessions[0])["id"].(string)
	if otherID == firstID {
		t.Fatalf("same native session id from a different source was incorrectly fused: %q", otherID)
	}
}

func sourceConversations(t *testing.T, server *httptest.Server, sourceID string) ([]any, string) {
	t.Helper()
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	if status != http.StatusOK {
		t.Fatalf("list conversations status=%d body=%+v", status, body)
	}
	sessions := foundryList(t, body["sessions"])
	revision := ""
	if len(sessions) > 0 {
		revision, _ = foundryObject(t, sessions[0])["revision"].(string)
	}
	return sessions, revision
}

func loadSourceHTTP(t *testing.T, server *httptest.Server, id string) map[string]any {
	t.Helper()
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/conversations/sources", nil)
	if status != http.StatusOK {
		t.Fatalf("list sources status=%d body=%+v", status, body)
	}
	for _, raw := range foundryList(t, body["sources"]) {
		source := foundryObject(t, raw)
		if source["id"] == id {
			return source
		}
	}
	t.Fatalf("source %q missing from list: %+v", id, body)
	return nil
}

func TestOpenStoreV2UpgradePreservesSourceLastScanMetadata(t *testing.T) {
	stateDir := t.TempDir()
	foundryDir := filepath.Join(stateDir, "foundry")
	if err := os.Mkdir(foundryDir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(foundryDir, "foundry.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO sources(id,path,adapter,enabled,created,last_capture,last_scan_at,last_scan_status,last_scan_files,last_scan_changed,last_scan_errors,last_scan_max_files,last_scan_current_file,last_scan_limited,coverage_json,last_error) VALUES('source-preserved','/tmp/foundry-preserved','canonical',1,10,11,1234.5,'succeeded',7,3,1,200,'capture.jsonl',0,'{"status":"complete"}','')`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := openStore(stateDir)
	if err != nil {
		t.Fatalf("open/migrate v2 store: %v", err)
	}
	defer migrated.Close()
	var lastScan sql.NullFloat64
	var status, current string
	var files, changed, scanErrors int
	if err := migrated.QueryRow(`SELECT last_scan_at,last_scan_status,last_scan_files,last_scan_changed,last_scan_errors,last_scan_current_file FROM sources WHERE id='source-preserved'`).Scan(&lastScan, &status, &files, &changed, &scanErrors, &current); err != nil {
		t.Fatal(err)
	}
	if !lastScan.Valid || lastScan.Float64 != 1234.5 || status != "succeeded" || files != 7 || changed != 3 || scanErrors != 1 || current != "capture.jsonl" {
		t.Fatalf("v2 migration altered scan receipt: at=%+v status=%q files=%d changed=%d errors=%d current=%q", lastScan, status, files, changed, scanErrors, current)
	}
}

func TestScanPersistsBoundedCaptureIssues(t *testing.T) {
	root := t.TempDir()
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	path := filepath.Join(root, "partial.jsonl")
	data := "{\"session_id\":\"partial-source\",\"role\":\"user\",\"kind\":\"message\",\"text\":\"Keep valid evidence\"}\n" + strings.Repeat("not-json\n", 205)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": path, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register partial source: status=%d body=%+v", status, body)
	}
	id := foundryObject(t, body["source"])["id"].(string)
	job := queueFoundryScan(t, server.Client(), server.URL, id)
	for _, coverage := range []map[string]any{
		foundryObject(t, foundryObject(t, job["result"])["coverage"]),
		foundryObject(t, loadSourceHTTP(t, server, id)["coverage"]),
	} {
		issues := foundryList(t, coverage["issues"])
		if coverage["status"] != "partial" || coverage["issue_count"] != float64(205) || len(issues) != 200 {
			t.Fatalf("partial diagnostics must retain totals and a bounded sample: %+v", coverage)
		}
		first := foundryObject(t, issues[0])
		if first["file_name"] != "partial.jsonl" || first["line"] != float64(2) || first["reason"] == "" {
			t.Fatalf("diagnostic lost its source, line or reason: %+v", first)
		}
	}
}

// One file above the published per-file limit: the scan must refuse it by size instead of
// reading it, and the fixture never depends on the host having that much free memory.
func writeOversizedJSONLFixture(t *testing.T, path string) {
	t.Helper()
	data := bytes.Repeat([]byte("x\n"), int(MaxSourceBytes/2)+1)
	if int64(len(data)) <= MaxSourceBytes {
		t.Fatalf("oversized fixture has %d bytes, want more than %d", len(data), MaxSourceBytes)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

// CV-05: the scan receipt states the conversations it really left captured, not the number
// of files it walked: a file refused by the byte limit counts zero, two files with two
// sessions count two, and an unchanged rescan neither inflates nor duplicates the count.
func TestScanResultCountsCapturedSessionsNotFiles(t *testing.T) {
	root := t.TempDir()
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	dir := filepath.Join(root, "mixed-source")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(dir, "a.jsonl"), map[string]any{"session_id": "cv05-sessions-a", "role": "user", "kind": "message", "text": "First captured conversation."})
	writeJSONLFixture(t, filepath.Join(dir, "b.jsonl"), map[string]any{"session_id": "cv05-sessions-b", "role": "user", "kind": "message", "text": "Second captured conversation."})
	writeOversizedJSONLFixture(t, filepath.Join(dir, "c-rejected.jsonl"))

	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": dir, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)

	result := foundryObject(t, queueFoundryScan(t, server.Client(), server.URL, sourceID)["result"])
	if result["sessions"] != float64(2) || result["files"] != float64(3) || result["errors"] != float64(1) {
		t.Fatalf("scan receipt must count captured sessions, not walked files: %+v", result)
	}
	if sessions, _ := sourceConversations(t, server, sourceID); len(sessions) != 2 {
		t.Fatalf("captured conversations = %d, want 2", len(sessions))
	}

	again := foundryObject(t, queueFoundryScan(t, server.Client(), server.URL, sourceID)["result"])
	if again["sessions"] != float64(2) || again["changed"] != float64(0) {
		t.Fatalf("unchanged rescan must not inflate the captured sessions: %+v", again)
	}
	if sessions, _ := sourceConversations(t, server, sourceID); len(sessions) != 2 {
		t.Fatalf("conversations after rescan = %d, want 2 without duplicates", len(sessions))
	}
}

// CV-05: a source whose only file is refused captures no conversation at all, and the
// receipt says zero instead of hiding the refusal behind the file count.
func TestScanResultReportsZeroSessionsForRejectedSource(t *testing.T) {
	root := t.TempDir()
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	path := filepath.Join(root, "large-over-limit.jsonl")
	writeOversizedJSONLFixture(t, path)

	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": path, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)
	job := queueFoundryScan(t, server.Client(), server.URL, sourceID)
	if job["status"] != "failed" {
		t.Fatalf("scan of a refused file status=%v, want failed", job["status"])
	}
	result := foundryObject(t, job["result"])
	if result["sessions"] != float64(0) || result["files"] != float64(1) || result["errors"] != float64(1) {
		t.Fatalf("refused source receipt = %+v, want zero captured sessions", result)
	}
	if sessions, _ := sourceConversations(t, server, sourceID); len(sessions) != 0 {
		t.Fatalf("refused source invented %d conversations", len(sessions))
	}
	coverage := foundryObject(t, result["coverage"])
	if coverage["status"] != "partial" || coverage["issue_count"] != float64(1) {
		t.Fatalf("refused source coverage = %+v", coverage)
	}
	issue := foundryObject(t, foundryList(t, coverage["issues"])[0])
	if issue["file_name"] != "large-over-limit.jsonl" || issue["reason"] != "source_byte_limit" {
		t.Fatalf("refusal must name its file and reason: %+v", issue)
	}
}

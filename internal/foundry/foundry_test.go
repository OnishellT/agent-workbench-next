// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"workbench/internal/hosts"
)

func newFoundryTestServer(t *testing.T, stateDir string, hostScope hosts.Service) (*Module, *httptest.Server) {
	t.Helper()
	module, err := New(Options{StateDir: stateDir, HostScope: hostScope})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(module.Handler())
	t.Cleanup(func() {
		server.Close()
		if err := module.Close(context.Background()); err != nil {
			t.Errorf("close Foundry: %v", err)
		}
	})
	return module, server
}

func callFoundryJSON(t *testing.T, client *http.Client, baseURL, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequest(method, baseURL+path, bytesReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode %s %s (%d): %v", method, path, response.StatusCode, err)
	}
	return response.StatusCode, result
}

func bytesReader(value []byte) *bytes.Reader { return bytes.NewReader(value) }

func foundryObject(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected JSON object, got %T (%v)", value, value)
	}
	return result
}

func foundryList(t *testing.T, value any) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("expected JSON array, got %T (%v)", value, value)
	}
	return result
}

func foundryErrorCode(t *testing.T, body map[string]any, want string) {
	t.Helper()
	if got := body["code"]; got != want {
		t.Fatalf("error code = %v, want %q (body=%+v)", got, want, body)
	}
}

func writeJSONLFixture(t *testing.T, path string, records ...map[string]any) {
	t.Helper()
	var data []byte
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, encoded...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func appendJSONLFixture(t *testing.T, path string, record map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitFoundryJob(t *testing.T, client *http.Client, baseURL, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, body := callFoundryJSON(t, client, baseURL, http.MethodGet, "/api/foundry/jobs/"+id, nil)
		if status != http.StatusOK {
			t.Fatalf("GET job status=%d body=%+v", status, body)
		}
		state := body["status"]
		if state == "succeeded" || state == "failed" || state == "cancelled" || state == "interrupted" {
			return body
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %q did not reach a terminal state", id)
	return nil
}

func queueFoundryScan(t *testing.T, client *http.Client, baseURL, sourceID string) map[string]any {
	t.Helper()
	status, body := callFoundryJSON(t, client, baseURL, http.MethodPost, "/api/conversations/sources/"+sourceID+"/scan", map[string]any{})
	if status != http.StatusAccepted {
		t.Fatalf("queue scan status=%d body=%+v", status, body)
	}
	return waitFoundryJob(t, client, baseURL, body["job_id"].(string))
}

func TestFoundryConversationTaskHTTPWorkflowAndImmutableStorage(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "consented-conversations")
	if err := os.Mkdir(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(sourceRoot, "transcript.jsonl")
	writeJSONLFixture(t, transcriptPath,
		map[string]any{"session_id": "session-A", "role": "user", "kind": "message", "text": "Please update the runtime."},
		map[string]any{"session_id": "session-A", "role": "assistant", "kind": "message", "text": "I inspected it; no code was changed."},
	)
	module, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	client := server.Client()

	status, data := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources/discover", map[string]any{"roots": []any{map[string]any{"path": sourceRoot, "adapter": "canonical"}}})
	if status != http.StatusOK || len(foundryList(t, data["candidates"])) != 1 {
		t.Fatalf("explicit discovery status=%d body=%+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations/sources", nil)
	if status != http.StatusOK || len(foundryList(t, data["sources"])) != 0 {
		t.Fatalf("discovery must not register sources: status=%d body=%+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations", nil)
	if status != http.StatusOK || len(foundryList(t, data["sessions"])) != 0 {
		t.Fatalf("discovery must not ingest conversations: status=%d body=%+v", status, data)
	}

	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, data)
	}
	sourceID := foundryObject(t, data["source"])["id"].(string)
	job := queueFoundryScan(t, client, server.URL, sourceID)
	if job["status"] != "succeeded" || foundryObject(t, job["progress"])["files"] != float64(1) {
		t.Fatalf("first scan expected succeeded / one file, observed %+v", job)
	}

	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	if status != http.StatusOK {
		t.Fatalf("list conversations status=%d body=%+v", status, data)
	}
	sessions := foundryList(t, data["sessions"])
	if len(sessions) != 1 {
		t.Fatalf("captured conversations: got %d want 1", len(sessions))
	}
	conversation := foundryObject(t, sessions[0])
	sessionID, rev1 := conversation["id"].(string), conversation["revision"].(string)
	if sessionID != "session-A" {
		t.Fatalf("canonical session id = %q, want session-A", sessionID)
	}
	if foundryObject(t, conversation["coverage"])["status"] != "complete" {
		t.Fatalf("coverage not complete: %+v", conversation["coverage"])
	}
	status, detail := callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations/"+sessionID+"/revisions/"+rev1+"?limit=100", nil)
	if status != http.StatusOK {
		t.Fatalf("read revision status=%d body=%+v", status, detail)
	}
	if foundryObject(t, detail["conversation"])["is_latest"] != true || len(foundryList(t, detail["events"])) != 2 {
		t.Fatalf("revision snapshot mismatch: %+v", detail)
	}

	baseTask := map[string]any{"session_id": sessionID, "revision": rev1, "first_event": 0, "last_event": 1, "title": "Update runtime", "citations": []any{map[string]any{"event_index": 0, "quote": "Please update the runtime"}}}
	badTask := map[string]any{"session_id": sessionID, "revision": rev1, "first_event": 0, "last_event": 1, "title": "Update runtime", "citations": []any{map[string]any{"event_index": 0, "quote": "not in source"}}}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks", badTask)
	if status != http.StatusBadRequest {
		t.Fatalf("wrong citation expected 400, observed status=%d body=%+v", status, data)
	}
	foundryErrorCode(t, data, "citation_not_found")
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks", baseTask)
	if status != http.StatusCreated {
		t.Fatalf("create task status=%d body=%+v", status, data)
	}
	task1 := foundryObject(t, data["task"])
	task1ID := task1["id"].(string)
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks", baseTask)
	if status != http.StatusOK || data["reused"] != true {
		t.Fatalf("same range should return idempotent task, status=%d body=%+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+task1ID+"/label", map[string]any{"label": "correct", "feedback": "Reviewed by a human", "expected_generation": 0})
	if status != http.StatusConflict {
		t.Fatalf("label before completion expected 409, observed status=%d body=%+v", status, data)
	}
	foundryErrorCode(t, data, "task_incomplete")
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+task1ID+"/completion", map[string]any{"completed": true, "expected_generation": 0})
	if status != http.StatusOK || foundryObject(t, data["task"])["completion_generation"] != float64(1) {
		t.Fatalf("human completion status=%d body=%+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+task1ID+"/label", map[string]any{"label": "correct", "feedback": "Reviewed by a human", "expected_generation": 1})
	if status != http.StatusOK || foundryObject(t, data["task"])["label"] != "correct" {
		t.Fatalf("manual label after completion status=%d body=%+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+task1ID+"/completion", map[string]any{"completed": false, "expected_generation": 1})
	if status != http.StatusOK || foundryObject(t, data["task"])["completion_generation"] != float64(2) || foundryObject(t, data["task"])["label"] != "pending" {
		t.Fatalf("reopen must revoke generation/reset label: status=%d body=%+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+task1ID+"/label", map[string]any{"label": "incorrect", "expected_generation": 1})
	if status != http.StatusConflict {
		t.Fatalf("stale task generation expected 409, observed status=%d body=%+v", status, data)
	}
	foundryErrorCode(t, data, "generation_conflict")

	appendJSONLFixture(t, transcriptPath, map[string]any{"session_id": "session-A", "role": "assistant", "kind": "message", "text": "The runtime has now been updated."})
	job = queueFoundryScan(t, client, server.URL, sourceID)
	if job["status"] != "succeeded" {
		t.Fatalf("append rescan expected succeeded, observed %+v", job)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	if status != http.StatusOK {
		t.Fatalf("list after append status=%d body=%+v", status, data)
	}
	conversation = foundryObject(t, foundryList(t, data["sessions"])[0])
	rev2 := conversation["revision"].(string)
	if conversation["id"] != sessionID || rev2 == rev1 {
		t.Fatalf("append must preserve session and add revision: before=%s/%s after=%+v", sessionID, rev1, conversation)
	}
	status, detail = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations/"+sessionID+"/revisions/"+rev1+"?limit=100", nil)
	if status != http.StatusOK || foundryObject(t, detail["conversation"])["is_latest"] != false || len(foundryList(t, detail["events"])) != 2 {
		t.Fatalf("old revision was changed or mislabelled: status=%d body=%+v", status, detail)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks", baseTask)
	if status != http.StatusConflict {
		t.Fatalf("historical task create expected 409, observed status=%d body=%+v", status, data)
	}
	foundryErrorCode(t, data, "stale_revision")
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+task1ID+"/completion", map[string]any{"completed": true, "expected_generation": 2})
	if status != http.StatusConflict {
		t.Fatalf("historical task completion expected 409, observed status=%d body=%+v", status, data)
	}
	foundryErrorCode(t, data, "stale_revision")

	task2Request := map[string]any{"session_id": sessionID, "revision": rev2, "first_event": 0, "last_event": 2, "title": "Update runtime from latest evidence", "citations": []any{map[string]any{"event_index": 0, "quote": "Please update the runtime"}}}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks", task2Request)
	if status != http.StatusCreated {
		t.Fatalf("create current task status=%d body=%+v", status, data)
	}
	task2ID := foundryObject(t, data["task"])["id"].(string)
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+task2ID+"/label", map[string]any{"label": "incorrect", "expected_generation": 0})
	if status != http.StatusConflict {
		t.Fatalf("task2 label before completion expected 409, observed status=%d body=%+v", status, data)
	}
	foundryErrorCode(t, data, "task_incomplete")
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+task2ID+"/completion", map[string]any{"completed": true, "expected_generation": 0})
	if status != http.StatusOK || foundryObject(t, data["task"])["completion_generation"] != float64(1) {
		t.Fatalf("task2 completion status=%d body=%+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+task2ID+"/label", map[string]any{"label": "incorrect", "expected_generation": 1})
	if status != http.StatusOK || foundryObject(t, data["task"])["label"] != "incorrect" {
		t.Fatalf("task2 label after completion status=%d body=%+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+task2ID+"/completion", map[string]any{"completed": false, "expected_generation": 1})
	if status != http.StatusOK || foundryObject(t, data["task"])["completion_generation"] != float64(2) || foundryObject(t, data["task"])["label"] != "pending" {
		t.Fatalf("task2 revoke status=%d body=%+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+task2ID+"/label", map[string]any{"label": "correct", "expected_generation": 1})
	if status != http.StatusConflict {
		t.Fatalf("stale label expected 409, observed status=%d body=%+v", status, data)
	}

	var completionHistory, labelHistory, revisions int
	if err := module.db.QueryRow("SELECT COUNT(*) FROM task_completion_history WHERE task_id=?", task2ID).Scan(&completionHistory); err != nil {
		t.Fatal(err)
	}
	if err := module.db.QueryRow("SELECT COUNT(*) FROM task_label_history WHERE task_id=?", task2ID).Scan(&labelHistory); err != nil {
		t.Fatal(err)
	}
	if err := module.db.QueryRow("SELECT COUNT(*) FROM revisions WHERE session_id=?", sessionID).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if completionHistory != 2 || labelHistory != 2 || revisions != 2 {
		t.Fatalf("append-only history counts completion=%d labels=%d revisions=%d; want 2/2/2", completionHistory, labelHistory, revisions)
	}
	if _, err := module.db.Exec("DELETE FROM events WHERE session_id=? AND revision=?", sessionID, rev1); err == nil {
		t.Fatal("immutable event rows accepted DELETE")
	}
	dbInfo, err := os.Stat(filepath.Join(root, "state", "foundry", "foundry.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if dbInfo.Mode().Perm()&0077 != 0 {
		t.Fatalf("Foundry database permissions = %o, want owner-only", dbInfo.Mode().Perm())
	}

	partialPath := filepath.Join(sourceRoot, "partial.jsonl")
	if err := os.WriteFile(partialPath, []byte("{\"session_id\":\"partial-session\",\"role\":\"user\",\"text\":\"unfinished\""), 0600); err != nil {
		t.Fatal(err)
	}
	job = queueFoundryScan(t, client, server.URL, sourceID)
	if job["status"] != "succeeded" {
		t.Fatalf("partial trailing record is coverage, not a scan crash: %+v", job)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID+"&status=error", nil)
	if status != http.StatusOK || len(foundryList(t, data["sessions"])) != 1 {
		t.Fatalf("error alias must list partial coverage: status=%d body=%+v", status, data)
	}
	partialConversation := foundryObject(t, foundryList(t, data["sessions"])[0])
	if foundryObject(t, partialConversation["coverage"])["status"] != "partial" {
		t.Fatalf("partial coverage was not exposed: %+v", partialConversation)
	}
	// The append-only record of the task is observable: every change of the chosen task
	// keeps its generation, its note and its own entry, and the older ones stay untouched.
	history := func(taskID string) ([]any, []any) {
		t.Helper()
		status, body := callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/tasks/"+taskID+"/history", nil)
		if status != http.StatusOK {
			t.Fatalf("history of %s status=%d body=%+v", taskID, status, body)
		}
		return foundryList(t, body["completions"]), foundryList(t, body["labels"])
	}
	completions, labels := history(task2ID)
	if len(completions) != 2 || len(labels) != 2 {
		t.Fatalf("task2 history got %d completions / %d labels, want 2/2: %+v %+v", len(completions), len(labels), completions, labels)
	}
	confirmed := foundryObject(t, completions[0])
	reopened := foundryObject(t, completions[1])
	if confirmed["generation"] != float64(1) || confirmed["completed"] != true || confirmed["confirmed_at"] == nil {
		t.Fatalf("first completion entry is not the confirmation it was: %+v", confirmed)
	}
	if reopened["generation"] != float64(2) || reopened["completed"] != false || reopened["revoked_generation"] != float64(1) {
		t.Fatalf("second completion entry does not revoke the first generation: %+v", reopened)
	}
	valued := foundryObject(t, labels[0])
	retracted := foundryObject(t, labels[1])
	if valued["generation"] != float64(1) || valued["action"] != "label" || valued["label"] != "incorrect" {
		t.Fatalf("the valuation entry is not the one written: %+v", valued)
	}
	if retracted["generation"] != float64(2) || retracted["action"] != "label" || retracted["label"] != "pending" || retracted["feedback"] != "" {
		t.Fatalf("reopening must retract the valuation in its own entry: %+v", retracted)
	}
	if _, err := module.db.Exec("DELETE FROM task_label_history WHERE task_id=?", task2ID); err == nil {
		t.Fatal("immutable task label history accepted DELETE")
	}
	if _, err := module.db.Exec("UPDATE task_completion_history SET generation=9 WHERE task_id=?", task2ID); err == nil {
		t.Fatal("immutable task completion history accepted UPDATE")
	}
	if after, _ := history(task2ID); len(after) != 2 {
		t.Fatalf("the history was rewritten: %+v", after)
	}

	// The two reopened tasks are the incomplete ones: a pending task is the one that is
	// already completed and not valued yet, which is what the history filter answers.
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/tasks?status=incomplete", nil)
	if status != http.StatusOK || len(foundryList(t, data["tasks"])) != 2 {
		t.Fatalf("reopened tasks must stay explicit and human-owned: status=%d body=%+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/tasks?status=pending", nil)
	if status != http.StatusOK || len(foundryList(t, data["tasks"])) != 0 {
		t.Fatalf("a reopened task is not a pending one: status=%d body=%+v", status, data)
	}
}

func TestFoundryExplicitSourceConsentAndSymlinkNegativeControls(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "consented-root")
	for _, dir := range []string{filepath.Join(sourceRoot, "a"), filepath.Join(sourceRoot, "b")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		writeJSONLFixture(t, filepath.Join(dir, "session.jsonl"), map[string]any{"session_id": "native-" + filepath.Base(dir), "role": "user", "kind": "message", "text": "Read this consented fixture."})
	}
	external := filepath.Join(root, "outside.jsonl")
	writeJSONLFixture(t, external, map[string]any{"session_id": "must-not-ingest", "role": "user", "text": "outside source"})
	if err := os.Symlink(external, filepath.Join(sourceRoot, "linked.jsonl")); err != nil {
		t.Fatal(err)
	}
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	client := server.Client()

	status, data := callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations/sources", nil)
	if status != http.StatusOK || len(foundryList(t, data["sources"])) != 0 {
		t.Fatalf("fresh Foundry registered or scanned a source: %d %+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/tasks/config", nil)
	if status != http.StatusOK {
		t.Fatalf("task config status=%d body=%+v", status, data)
	}
	if _, ok := foundryObject(t, data["config"])["capture"]; ok {
		t.Fatalf("retired global capture leaked in task config: %+v", data)
	}

	rootLink := filepath.Join(root, "root-link")
	if err := os.Symlink(sourceRoot, rootLink); err != nil {
		t.Fatal(err)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": rootLink, "adapter": "canonical"})
	if status != http.StatusBadRequest {
		t.Fatalf("symlink source root expected 400, observed %d %+v", status, data)
	}
	foundryErrorCode(t, data, "unsupported_source")
	parentLink := filepath.Join(root, "parent-link")
	if err := os.Symlink(sourceRoot, parentLink); err != nil {
		t.Fatal(err)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": filepath.Join(parentLink, "a"), "adapter": "canonical"})
	if status != http.StatusBadRequest {
		t.Fatalf("source beneath symlinked parent expected 400, observed %d %+v", status, data)
	}
	foundryErrorCode(t, data, "unsupported_source")

	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources/discover", map[string]any{"roots": []any{map[string]any{"path": sourceRoot, "adapter": "canonical"}}})
	if status != http.StatusOK || len(foundryList(t, data["candidates"])) != 1 {
		t.Fatalf("explicit discovery status=%d body=%+v", status, data)
	}
	if len(foundryList(t, data["issues"])) == 0 {
		t.Fatalf("symlink skip should be visible in discovery: %+v", data)
	}
	_, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations/sources", nil)
	if len(foundryList(t, data["sources"])) != 0 {
		t.Fatalf("discovery auto-registered source: %+v", data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register explicit source status=%d body=%+v", status, data)
	}
	sourceID := foundryObject(t, data["source"])["id"].(string)
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPatch, "/api/conversations/sources/"+sourceID, map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("disable source status=%d body=%+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources/"+sourceID+"/scan", map[string]any{})
	if status != http.StatusConflict {
		t.Fatalf("disabled source scan expected 409, observed %d %+v", status, data)
	}
	foundryErrorCode(t, data, "source_revoked")
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPatch, "/api/conversations/sources/"+sourceID, map[string]any{"enabled": true})
	if status != http.StatusOK {
		t.Fatalf("enable explicit source status=%d body=%+v", status, data)
	}
	job := queueFoundryScan(t, client, server.URL, sourceID)
	if job["status"] != "failed" {
		t.Fatalf("scan with a skipped symlink expected an explicit partial/failure status, got %+v", job)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	if status != http.StatusOK {
		t.Fatalf("list scan results status=%d body=%+v", status, data)
	}
	sessions := foundryList(t, data["sessions"])
	if len(sessions) != 2 {
		t.Fatalf("only regular consented files should be captured, got %d: %+v", len(sessions), sessions)
	}
	ids := map[string]bool{}
	for _, item := range sessions {
		id := foundryObject(t, item)["id"].(string)
		if ids[id] {
			t.Fatalf("same basename/session id collided at %q", id)
		}
		ids[id] = true
	}
	if ids["must-not-ingest"] {
		t.Fatal("symlink target outside source root was ingested")
	}
	_, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations/sources", nil)
	source := foundryObject(t, foundryList(t, data["sources"])[0])
	if foundryObject(t, source["coverage"])["status"] != "partial" {
		t.Fatalf("skipped symlink must lower source coverage: %+v", source)
	}
}

type blockingHostScope struct {
	started chan struct{}
	once    sync.Once
}

func (b *blockingHostScope) List(context.Context) ([]hosts.Host, error) { return nil, nil }
func (b *blockingHostScope) Get(ctx context.Context, id string) (hosts.Host, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return hosts.Host{}, ctx.Err()
}
func (b *blockingHostScope) Identity(context.Context, string) (hosts.Identity, error) {
	return hosts.Identity{}, nil
}
func (b *blockingHostScope) SSHOperation(context.Context, string, hosts.Operation) error { return nil }

type heldHostScope struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func (h *heldHostScope) List(context.Context) ([]hosts.Host, error) { return nil, nil }
func (h *heldHostScope) Get(context.Context, string) (hosts.Host, error) {
	h.startedOnce.Do(func() { close(h.started) })
	<-h.release // Deliberately ignore cancellation to hold shutdown until the test releases it.
	return hosts.Host{}, nil
}
func (h *heldHostScope) Identity(context.Context, string) (hosts.Identity, error) {
	return hosts.Identity{}, nil
}
func (h *heldHostScope) SSHOperation(context.Context, string, hosts.Operation) error { return nil }
func (h *heldHostScope) unblock()                                                    { h.releaseOnce.Do(func() { close(h.release) }) }

func TestFoundryCloseWaitsForJobsAndConcurrentCallers(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "consented-root")
	if err := os.Mkdir(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "held.jsonl"), map[string]any{"session_id": "held-session", "role": "user", "kind": "message", "text": "held shutdown fixture", "host_id": "held-test"})
	hostScope := &heldHostScope{started: make(chan struct{}), release: make(chan struct{})}
	module, server := newFoundryTestServer(t, filepath.Join(root, "state"), hostScope)
	defer server.Close()
	t.Cleanup(hostScope.unblock)
	client := server.Client()

	status, data := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register held scan source status=%d body=%+v", status, data)
	}
	sourceID := foundryObject(t, data["source"])["id"].(string)
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources/"+sourceID+"/scan", map[string]any{})
	if status != http.StatusAccepted {
		t.Fatalf("queue held scan status=%d body=%+v", status, data)
	}
	select {
	case <-hostScope.started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker never reached held host-scope gate")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	closeErr := module.Close(ctx)
	cancel()
	if !errors.Is(closeErr, ErrShutdownUnconfirmed) || !errors.Is(closeErr, context.DeadlineExceeded) {
		t.Fatalf("Close with held job expected unconfirmed deadline, observed %v", closeErr)
	}
	if err := module.db.PingContext(context.Background()); err != nil {
		t.Fatalf("Foundry DB must remain open until active job drains: %v", err)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/scan", map[string]any{"source_id": sourceID})
	if status != http.StatusServiceUnavailable || data["code"] != "closed" {
		t.Fatalf("job admission after Close expected 503 closed, observed status=%d body=%+v", status, data)
	}

	started := make(chan struct{}, 2)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			started <- struct{}{}
			results <- module.Close(context.Background())
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("concurrent Close caller did not start")
		}
	}
	select {
	case err := <-results:
		t.Fatalf("Close returned before the held job drained: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	if err := module.db.PingContext(context.Background()); err != nil {
		t.Fatalf("Foundry DB closed before the held job drained: %v", err)
	}

	hostScope.unblock()
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("concurrent Close returned %v after drain", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Close did not finish after the held job drained")
		}
	}
	if err := module.db.PingContext(context.Background()); err == nil {
		t.Fatal("Foundry DB must be closed after the active job drains")
	}
	if err := module.Close(context.Background()); err != nil {
		t.Fatalf("subsequent Close must observe the shared completion result: %v", err)
	}
}

func TestFoundryCancelStopsAnInFlightHostScopedScan(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "consented-root")
	if err := os.Mkdir(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "remote.jsonl"), map[string]any{"session_id": "remote-session", "role": "user", "kind": "message", "text": "remote history fixture", "host_id": "remote-test"})
	hostScope := &blockingHostScope{started: make(chan struct{})}
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), hostScope)
	client := server.Client()
	status, data := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register host-scoped source status=%d body=%+v", status, data)
	}
	sourceID := foundryObject(t, data["source"])["id"].(string)
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources/"+sourceID+"/scan", map[string]any{})
	if status != http.StatusAccepted {
		t.Fatalf("queue scan status=%d body=%+v", status, data)
	}
	jobID := data["job_id"].(string)
	select {
	case <-hostScope.started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker never reached the host-scope gate")
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/foundry/jobs/"+jobID+"/cancel", map[string]any{})
	if status != http.StatusAccepted {
		t.Fatalf("cancel status=%d body=%+v", status, data)
	}
	job := waitFoundryJob(t, client, server.URL, jobID)
	if job["status"] != "cancelled" {
		t.Fatalf("in-flight scan cancellation expected cancelled, observed %+v", job)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations", nil)
	if status != http.StatusOK || len(foundryList(t, data["sessions"])) != 0 {
		t.Fatalf("cancelled host scan must not persist transcript, status=%d body=%+v", status, data)
	}
}

func TestFoundryBootDoesNotScanAnExistingEnabledSource(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	sourceRoot := filepath.Join(root, "explicit-root")
	if err := os.Mkdir(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "session.jsonl"), map[string]any{"session_id": "boot-must-not-scan", "role": "user", "kind": "message", "text": "explicit source fixture"})
	first, err := New(Options{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	firstServer := httptest.NewServer(first.Handler())
	status, data := callFoundryJSON(t, firstServer.Client(), firstServer.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("explicit source creation status=%d body=%+v", status, data)
	}
	firstServer.Close()
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	restarted, err := New(Options{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close(context.Background())
	var scanJobs, sessions int
	if err := restarted.db.QueryRow("SELECT COUNT(*) FROM jobs WHERE kind='scan'").Scan(&scanJobs); err != nil {
		t.Fatal(err)
	}
	if err := restarted.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	var enabled int
	if err := restarted.db.QueryRow("SELECT enabled FROM sources WHERE path=?", sourceRoot).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if scanJobs != 0 || sessions != 0 || enabled != 1 {
		t.Fatalf("boot performed unauthorized source work: scanJobs=%d sessions=%d enabled=%d", scanJobs, sessions, enabled)
	}
}

func TestDisablingSourceCancelsInFlightScanBeforePersistence(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "consented-root")
	if err := os.Mkdir(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "remote.jsonl"), map[string]any{"session_id": "revoked-session", "role": "user", "kind": "message", "text": "must not persist after revocation", "host_id": "revocation-test"})
	hostScope := &blockingHostScope{started: make(chan struct{})}
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), hostScope)
	client := server.Client()
	status, data := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register explicit source status=%d body=%+v", status, data)
	}
	sourceID := foundryObject(t, data["source"])["id"].(string)
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources/"+sourceID+"/scan", map[string]any{})
	if status != http.StatusAccepted {
		t.Fatalf("queue explicit scan status=%d body=%+v", status, data)
	}
	jobID := data["job_id"].(string)
	select {
	case <-hostScope.started:
	case <-time.After(3 * time.Second):
		t.Fatal("scan never reached the host-scope authorization gate")
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPatch, "/api/conversations/sources/"+sourceID, map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("revoke source during scan status=%d body=%+v", status, data)
	}
	job := waitFoundryJob(t, client, server.URL, jobID)
	if job["status"] != "cancelled" {
		t.Fatalf("revoked in-flight scan expected cancelled, got %+v", job)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations", nil)
	if status != http.StatusOK || len(foundryList(t, data["sessions"])) != 0 {
		t.Fatalf("revoked source must not publish after disable returns, status=%d body=%+v", status, data)
	}
}

func TestFoundryEmbeddedExamplePersistsInInstanceStoreWithoutCollisions(t *testing.T) {
	root := t.TempDir()
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	run := func() map[string]any {
		status, data := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/foundry/example", map[string]any{})
		if status != http.StatusOK || data["status"] != "succeeded" {
			t.Fatalf("embedded same-store fixture expected succeeded, observed status=%d body=%+v", status, data)
		}
		checks := foundryList(t, data["checks"])
		if len(checks) < 10 {
			t.Fatalf("expected substantive same-store checks, observed %d: %+v", len(checks), checks)
		}
		for _, value := range checks {
			check := foundryObject(t, value)
			if check["status"] != "passed" {
				t.Fatalf("fixture check failed: %+v", check)
			}
			// The example never takes a human decision: it only states the gate stays free.
			switch check["name"] {
			case "human_completion", "human_label", "completion_generation", "reopen_request", "reopen_revokes_completion", "reopen_resets_label", "stale_generation_rejected":
				t.Fatalf("the example must not run human gates: %+v", check)
			}
		}
		return data
	}
	first := run()
	second := run()
	firstSession, firstTask := first["session_id"].(string), first["task_id"].(string)
	secondSession, secondTask := second["session_id"].(string), second["task_id"].(string)
	if firstSession == "" || firstTask == "" || secondSession == "" || secondTask == "" || firstSession == secondSession || firstTask == secondTask {
		t.Fatalf("repeated embedded examples must produce distinct, nonempty IDs; first=(%q,%q) second=(%q,%q)", firstSession, firstTask, secondSession, secondTask)
	}
	for _, pair := range [][2]string{{firstSession, firstTask}, {secondSession, secondTask}} {
		status, data := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/conversations/"+pair[0], nil)
		if status != http.StatusOK || foundryObject(t, data["conversation"])["id"] != pair[0] {
			t.Fatalf("example conversation %q was not visible in this handler/store: status=%d body=%+v", pair[0], status, data)
		}
		status, data = callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/tasks/"+pair[1], nil)
		if status != http.StatusOK || data["id"] != pair[1] {
			t.Fatalf("example task %q was not visible in this handler/store: status=%d body=%+v", pair[1], status, data)
		}
		// The task of the example waits for the human with an untouched record.
		if data["status"] != "incomplete" || data["label"] != "pending" || data["completed"] != false || data["completion_generation"] != float64(0) {
			t.Fatalf("example task %q is not waiting for a human decision: %+v", pair[1], data)
		}
		status, data = callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/tasks/"+pair[1]+"/history", nil)
		if status != http.StatusOK || len(foundryList(t, data["completions"])) != 0 || len(foundryList(t, data["labels"])) != 0 {
			t.Fatalf("example task %q carries decisions nobody made: status=%d body=%+v", pair[1], status, data)
		}
	}
}

// The private HTTP run of the example serves the real store on a loopback port, so it is
// closed to every other client: without the capability of that run nothing is read or written.
func TestEmbeddedExampleRunIsClosedToOtherClients(t *testing.T) {
	root := t.TempDir()
	module, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	capability := "cap_fixture_private"
	run := httptest.NewServer(exampleGuard(module.Handler(), capability))
	defer run.Close()

	// Another local client that guesses the port still cannot read the store.
	status, data := callFoundryJSON(t, server.Client(), run.URL, http.MethodGet, "/api/conversations", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("an unauthorized read expected 401, observed status=%d body=%+v", status, data)
	}
	foundryErrorCode(t, data, "example_capability_required")
	request, err := http.NewRequest(http.MethodPost, run.URL+"/api/tasks", strings.NewReader(`{"session_id":"x","revision":"y","first_event":0,"last_event":0}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an unauthorized write expected 401, observed status=%d", response.StatusCode)
	}
	status, data = callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/tasks", nil)
	if status != http.StatusOK || len(foundryList(t, data["tasks"])) != 0 {
		t.Fatalf("the refused run must not touch the store: status=%d body=%+v", status, data)
	}

	// The run itself carries its capability and works as before.
	called := false
	guarded := exampleGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }), capability)
	request, err = http.NewRequest(http.MethodGet, "/api/conversations", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Example-Capability", capability)
	recorder := httptest.NewRecorder()
	guarded.ServeHTTP(recorder, request)
	if !called || recorder.Code != http.StatusNoContent {
		t.Fatalf("the run's own capability was not accepted: called=%v code=%d", called, recorder.Code)
	}
}

// CV-09: reading one immutable revision in full returns the evidence the capture stored
// (model, usage, native ids, metadata of the adapter sample), while the compact read keeps
// the same events bounded without inventing or dropping their provenance.
func TestRevisionFullReadExposesPreservedEvidence(t *testing.T) {
	root := t.TempDir()
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	path := filepath.Join(root, "claude.jsonl")
	writeJSONLFixture(t, path,
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "cv09-evidence-claude", "cwd": "/cv09-fixtures/claude", "version": "fixture-v1", "source": "claude-fixture", "api_key": "canary-cv07-meta-secret"}},
		map[string]any{"type": "user", "uuid": "cv09-evidence-u", "sessionId": "cv09-evidence-claude", "timestamp": "2026-10-17T10:00:00Z", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "question claude"}}}},
		map[string]any{"type": "assistant", "uuid": "cv09-evidence-a", "sessionId": "cv09-evidence-claude", "timestamp": "2026-10-17T10:00:01Z", "api_key": "canary-cv07-secret-value", "message": map[string]any{"role": "assistant", "model": "fixture-claude", "usage": map[string]any{"input_tokens": 11, "output_tokens": 3}, "content": []any{map[string]any{"type": "text", "text": "answer claude"}}}},
	)
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": path, "adapter": "claude"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)
	queueFoundryScan(t, server.Client(), server.URL, sourceID)
	sessions, revision := sourceConversations(t, server, sourceID)
	if len(sessions) != 1 || revision == "" {
		t.Fatalf("claude sample sessions=%d revision=%q", len(sessions), revision)
	}
	sessionID := foundryObject(t, sessions[0])["id"].(string)
	base := "/api/conversations/" + sessionID + "/revisions/" + revision + "?limit=100"

	status, full := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, base, nil)
	if status != http.StatusOK {
		t.Fatalf("full read status=%d body=%+v", status, full)
	}
	events := foundryList(t, full["events"])
	if len(events) != 3 {
		t.Fatalf("claude sample events=%d, want metadata+user+assistant", len(events))
	}
	metadata := foundryObject(t, foundryObject(t, events[0])["metadata"])
	if foundryObject(t, events[0])["kind"] != "session_metadata" || metadata["cwd"] != "/cv09-fixtures/claude" || metadata["version"] != "fixture-v1" {
		t.Fatalf("session metadata evidence missing: %+v", events[0])
	}
	assistant := foundryObject(t, events[2])
	if assistant["role"] != "assistant" || assistant["text"] != "answer claude" {
		t.Fatalf("assistant event = %+v", assistant)
	}
	if assistant["id_origin"] != "native" || assistant["line"] != float64(3) {
		t.Fatalf("native id/line provenance missing: %+v", assistant)
	}
	message := foundryObject(t, foundryObject(t, assistant["raw_record"])["message"])
	if message["model"] != "fixture-claude" {
		t.Fatalf("captured model missing from the evidence: %+v", message)
	}
	usage := foundryObject(t, message["usage"])
	if usage["input_tokens"] != float64(11) || usage["output_tokens"] != float64(3) {
		t.Fatalf("captured usage missing from the evidence: %+v", usage)
	}

	// The exposed evidence keeps the redaction boundary of the capture: a fake secret in
	// the metadata and in the raw record never reaches the API body.
	encoded, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"canary-cv07-secret-value", "canary-cv07-meta-secret"} {
		if bytes.Contains(encoded, []byte(canary)) {
			t.Fatalf("canary %q leaked through the full read: %s", canary, encoded)
		}
	}
	if !bytes.Contains(encoded, []byte("REDACTED")) {
		t.Fatalf("redaction marker missing from the exposed evidence: %s", encoded)
	}

	status, compact := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, base+"&compact=1", nil)
	if status != http.StatusOK {
		t.Fatalf("compact read status=%d body=%+v", status, compact)
	}
	compactEvents := foundryList(t, compact["events"])
	if len(compactEvents) != 3 {
		t.Fatalf("compact read events=%d, want 3", len(compactEvents))
	}
	compactAssistant := foundryObject(t, compactEvents[2])
	if compactAssistant["event_index"] != float64(2) || compactAssistant["text"] != "answer claude" {
		t.Fatalf("compact read lost the canonical fields: %+v", compactAssistant)
	}
	if _, exists := compactAssistant["raw_record"]; exists {
		t.Fatalf("compact read must stay bounded without the raw evidence: %+v", compactAssistant)
	}
}

// CV-09: switching the adapter of a source invalidates the captured revision instead of
// serving the events of the previous adapter, and the conversation keeps its identity.
func TestAdapterChangeCreatesNewRevisionWithoutDuplicatingSession(t *testing.T) {
	root := t.TempDir()
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	path := filepath.Join(root, "codex.jsonl")
	writeJSONLFixture(t, path,
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "cv09-switch-codex", "cwd": "/cv09-fixtures/codex", "version": "fixture-v1", "source": "codex-fixture"}},
		map[string]any{"type": "item_completed", "payload": map[string]any{"item": map[string]any{"id": "cv09-switch-u", "type": "UserMessage", "text": "question codex", "timestamp": "2026-10-17T10:01:00Z"}}},
		map[string]any{"type": "item_completed", "payload": map[string]any{"item": map[string]any{"id": "cv09-switch-a", "type": "AgentMessage", "text": "answer codex", "channel": "final", "timestamp": "2026-10-17T10:01:01Z"}}},
	)
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": path, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)

	// The canonical adapter cannot read these records: it retains them as raw-only events.
	first := queueFoundryScan(t, server.Client(), server.URL, sourceID)
	if first["status"] != "succeeded" {
		t.Fatalf("first scan status=%v body=%+v", first["status"], first)
	}
	sessions, firstRevision := sourceConversations(t, server, sourceID)
	if len(sessions) != 1 || firstRevision == "" {
		t.Fatalf("first scan sessions=%d revision=%q", len(sessions), firstRevision)
	}
	sessionID := foundryObject(t, sessions[0])["id"].(string)
	if _, detail := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/conversations/"+sessionID+"/revisions/"+firstRevision+"?limit=100&compact=1", nil); foundryObject(t, foundryList(t, detail["events"])[0])["role"] != "service" {
		t.Fatalf("canonical adapter was expected to read these records as raw events: %+v", detail["events"])
	}

	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/conversations/sources/"+sourceID, map[string]any{"adapter": "codex"})
	if status != http.StatusOK {
		t.Fatalf("switch adapter status=%d body=%+v", status, body)
	}
	second := queueFoundryScan(t, server.Client(), server.URL, sourceID)
	result := foundryObject(t, second["result"])
	if result["changed"] != float64(1) || result["sessions"] != float64(1) {
		t.Fatalf("adapter change must capture a new revision once: %+v", result)
	}

	sessions, secondRevision := sourceConversations(t, server, sourceID)
	if len(sessions) != 1 {
		t.Fatalf("adapter change duplicated the conversation: %d sessions", len(sessions))
	}
	if foundryObject(t, sessions[0])["id"] != sessionID {
		t.Fatalf("adapter change replaced the conversation identity: %+v", sessions[0])
	}
	if secondRevision == firstRevision {
		t.Fatalf("adapter change reused the revision captured with the previous adapter: %q", secondRevision)
	}
	_, detail := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/conversations/"+sessionID+"/revisions/"+secondRevision+"?limit=100&compact=1", nil)
	events := foundryList(t, detail["events"])
	if len(events) != 3 || foundryObject(t, events[1])["role"] != "user" || foundryObject(t, events[2])["role"] != "assistant" {
		t.Fatalf("codex adapter did not capture the messages after the switch: %+v", events)
	}
	// The revision captured with the previous adapter stays readable: no provenance loss.
	status, older := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/conversations/"+sessionID+"/revisions/"+firstRevision+"?limit=100&compact=1", nil)
	if status != http.StatusOK || len(foundryList(t, older["events"])) != 3 {
		t.Fatalf("previous revision became unreadable after the adapter change: %d %+v", status, older)
	}
}

// CV-09: a record that wraps its content in `message` (Claude and Pi logs) keeps its own
// native identifiers and session on the captured event instead of becoming a generated id.
func TestWrappedMessageKeepsNativeIdentity(t *testing.T) {
	root := t.TempDir()
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	path := filepath.Join(root, "claude.jsonl")
	writeJSONLFixture(t, path,
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "cv09-native-claude", "cwd": "/cv09-fixtures/claude", "version": "fixture-v1", "source": "claude-fixture"}},
		map[string]any{"type": "user", "uuid": "cv09-native-u", "sessionId": "cv09-native-claude", "timestamp": "2026-10-17T10:00:00Z", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "question claude"}}}},
		map[string]any{"type": "assistant", "uuid": "cv09-native-a", "sessionId": "cv09-native-claude", "timestamp": "2026-10-17T10:00:01Z", "message": map[string]any{"role": "assistant", "model": "fixture-claude", "content": []any{map[string]any{"type": "text", "text": "answer claude"}}}},
	)
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": path, "adapter": "claude"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)
	queueFoundryScan(t, server.Client(), server.URL, sourceID)
	sessions, revision := sourceConversations(t, server, sourceID)
	if len(sessions) != 1 || revision == "" {
		t.Fatalf("claude sample sessions=%d revision=%q", len(sessions), revision)
	}
	sessionID := foundryObject(t, sessions[0])["id"].(string)
	status, detail := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/conversations/"+sessionID+"/revisions/"+revision+"?limit=100", nil)
	if status != http.StatusOK {
		t.Fatalf("full read status=%d body=%+v", status, detail)
	}
	assistant := foundryObject(t, foundryList(t, detail["events"])[2])
	if assistant["id"] != "cv09-native-a" || assistant["id_origin"] != "native" {
		t.Fatalf("wrapped message lost its native id: %+v", assistant)
	}
	if assistant["session_id"] != "cv09-native-claude" || assistant["session_id_origin"] != "native" {
		t.Fatalf("wrapped message lost its native session: %+v", assistant)
	}
	if assistant["timestamp"] != "2026-10-17T10:00:01Z" {
		t.Fatalf("wrapped message lost its own timestamp: %+v", assistant)
	}
}

// CV-07: walking one revision with the published next_cursor returns every event exactly
// once and in order, both in the compact and in the full read. The cursor follows the last
// delivered event, so the look-ahead row of the page is never skipped.
func TestRevisionPaginationWalksEveryEventOnce(t *testing.T) {
	root := t.TempDir()
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	path := filepath.Join(root, "paged.jsonl")
	records := []map[string]any{{"type": "session_meta", "payload": map[string]any{"id": "cv07-paged", "cwd": "/cv07", "version": "fixture-v1", "source": "cv07-fixture"}}}
	for index := 0; index < 5; index++ {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		records = append(records, map[string]any{"session_id": "cv07-paged", "id": fmt.Sprintf("cv07-paged-%d", index), "role": role, "kind": "message", "text": fmt.Sprintf("evento %d", index), "sequence": index})
	}
	writeJSONLFixture(t, path, records...)
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": path, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)
	queueFoundryScan(t, server.Client(), server.URL, sourceID)
	sessions, revision := sourceConversations(t, server, sourceID)
	if len(sessions) != 1 || revision == "" {
		t.Fatalf("paged sample sessions=%d revision=%q", len(sessions), revision)
	}
	sessionID := foundryObject(t, sessions[0])["id"].(string)

	for _, compact := range []bool{true, false} {
		query := "?limit=2"
		if compact {
			query += "&compact=1"
		}
		collected := []int{}
		cursor := ""
		pages := 0
		for {
			target := "/api/conversations/" + sessionID + "/revisions/" + revision + query
			if cursor != "" {
				target += "&cursor=" + cursor
			}
			status, page := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, target, nil)
			if status != http.StatusOK {
				t.Fatalf("compact=%v page %d status=%d body=%+v", compact, pages, status, page)
			}
			for _, raw := range foundryList(t, page["events"]) {
				collected = append(collected, int(foundryObject(t, raw)["event_index"].(float64)))
			}
			pages++
			if pages > 10 {
				t.Fatalf("compact=%v pagination did not terminate: %v", compact, collected)
			}
			cursor, _ = page["next_cursor"].(string)
			if cursor == "" {
				break
			}
		}
		if len(collected) != len(records) {
			t.Fatalf("compact=%v walked %d events, want %d: %v", compact, len(collected), len(records), collected)
		}
		for position, index := range collected {
			if index != position {
				t.Fatalf("compact=%v page walk is not ordered without gaps: position %d has event_index %d (%v)", compact, position, index, collected)
			}
		}
	}
}

// The search of the list is the literal text the operator wrote: %, _ and \ of a title are
// characters of it, so the three fixtures that differ exactly in that character must answer
// their own row and nothing else.
func TestConversationSearchKeepsWildcardsLiteral(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "consented-search")
	if err := os.Mkdir(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "underscore.jsonl"),
		map[string]any{"session_id": "search-underscore", "role": "user", "kind": "message", "text": "binary_search index"},
	)
	writeJSONLFixture(t, filepath.Join(sourceRoot, "space.jsonl"),
		map[string]any{"session_id": "search-space", "role": "user", "kind": "message", "text": "binary search index"},
	)
	writeJSONLFixture(t, filepath.Join(sourceRoot, "backslash.jsonl"),
		map[string]any{"session_id": "search-backslash", "role": "user", "kind": "message", "text": `binary\search index`},
	)
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	client := server.Client()
	status, data := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, data)
	}
	if job := queueFoundryScan(t, client, server.URL, foundryObject(t, data["source"])["id"].(string)); foundryObject(t, job["progress"])["files"] != float64(3) {
		t.Fatalf("scan of the three fixtures: %+v", job)
	}
	search := func(query string) map[string]bool {
		t.Helper()
		status, body := callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations?q="+query, nil)
		if status != http.StatusOK {
			t.Fatalf("search %q status=%d body=%+v", query, status, body)
		}
		found := map[string]bool{}
		for _, raw := range foundryList(t, body["sessions"]) {
			found[foundryObject(t, raw)["id"].(string)] = true
		}
		return found
	}
	if found := search("binary"); len(found) != 3 {
		t.Fatalf("plain search answered %v, want the three conversations", found)
	}
	found := search("binary_search")
	if len(found) != 1 || !found["search-underscore"] {
		t.Fatalf("search of a literal underscore answered %v, want only search-underscore", found)
	}
	if found := search("%25"); len(found) != 0 {
		t.Fatalf("search of a literal %% answered %v, want no conversation", found)
	}
	// A literal backslash is the character itself, not the escape of the pattern.
	if found := search("binary%5Csearch"); len(found) != 1 || !found["search-backslash"] {
		t.Fatalf("search of a literal backslash answered %v, want only search-backslash", found)
	}
}

// The task search is the literal text the operator wrote over title and feedback: %, _ and
// \ of a task answer their own row, so the four tasks below stay distinguishable.
func TestTaskSearchKeepsWildcardsLiteral(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "consented-tasks")
	if err := os.Mkdir(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	for index := 0; index < 4; index++ {
		records = append(records,
			map[string]any{"session_id": "task-search", "role": "user", "kind": "message", "text": "petición humana del operador"},
			map[string]any{"session_id": "task-search", "role": "assistant", "kind": "message", "text": "respuesta"},
		)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "session.jsonl"), records...)
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	client := server.Client()
	status, data := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, data)
	}
	sourceID := foundryObject(t, data["source"])["id"].(string)
	if job := queueFoundryScan(t, client, server.URL, sourceID); job["status"] != "succeeded" {
		t.Fatalf("scan of the task fixture: %+v", job)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	if status != http.StatusOK {
		t.Fatalf("list conversations status=%d body=%+v", status, data)
	}
	conversation := foundryObject(t, foundryList(t, data["sessions"])[0])
	sessionID, revision := conversation["id"].(string), conversation["revision"].(string)

	// One task per human event, each with the character the search below distinguishes by.
	created := []struct {
		event int
		title string
	}{
		{0, "literal_normal busca"},
		{2, "literal normal busca"},
		{4, "literal%normal busca"},
		{6, `literal\normal busca`},
	}
	ids := map[string]string{}
	for _, task := range created {
		status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks", map[string]any{"session_id": sessionID, "revision": revision, "first_event": task.event, "last_event": task.event, "title": task.title})
		if status != http.StatusCreated {
			t.Fatalf("create task %q status=%d body=%+v", task.title, status, data)
		}
		ids[task.title] = foundryObject(t, data["task"])["id"].(string)
	}
	// The feedback is searched with the same literal rule as the title.
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+ids["literal normal busca"]+"/feedback", map[string]any{"feedback": "nota del operador sin comodines"})
	if status != http.StatusOK {
		t.Fatalf("feedback of the plain task status=%d body=%+v", status, data)
	}

	search := func(query string) map[string]string {
		t.Helper()
		status, body := callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/tasks?q="+query, nil)
		if status != http.StatusOK {
			t.Fatalf("search %q status=%d body=%+v", query, status, body)
		}
		found := map[string]string{}
		for _, raw := range foundryList(t, body["tasks"]) {
			task := foundryObject(t, raw)
			found[task["id"].(string)] = task["title"].(string)
		}
		return found
	}
	only := func(query, title string) {
		t.Helper()
		found := search(query)
		if len(found) != 1 || found[ids[title]] != title {
			t.Fatalf("search %q answered %v, want only %q", query, found, title)
		}
	}
	if found := search("literal"); len(found) != 4 {
		t.Fatalf("plain search answered %v, want the four tasks", found)
	}
	only("literal_normal", "literal_normal busca")
	only("literal%25normal", "literal%normal busca")
	only("literal%5Cnormal", `literal\normal busca`)
	only("%25", "literal%normal busca")
	only("_", "literal_normal busca")
	only("nota%20del%20operador", "literal normal busca")
}

// Two callers sending the same new range at the same time are one task: the first one
// creates it and the second one reuses it, so the range never gets two rows.
func TestConcurrentTaskCreateReusesTheSameRange(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "consented-concurrent")
	if err := os.Mkdir(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	var concurrentRecords []map[string]any
	for index := 0; index < 10; index++ {
		concurrentRecords = append(concurrentRecords,
			map[string]any{"session_id": "task-concurrent", "role": "user", "kind": "message", "text": fmt.Sprintf("petición humana %d del rango nuevo", index)},
			map[string]any{"session_id": "task-concurrent", "role": "assistant", "kind": "message", "text": fmt.Sprintf("respuesta %d", index)},
		)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "session.jsonl"), concurrentRecords...)
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	client := server.Client()
	status, data := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, data)
	}
	sourceID := foundryObject(t, data["source"])["id"].(string)
	if job := queueFoundryScan(t, client, server.URL, sourceID); job["status"] != "succeeded" {
		t.Fatalf("scan of the concurrent fixture: %+v", job)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	if status != http.StatusOK {
		t.Fatalf("list conversations status=%d body=%+v", status, data)
	}
	conversation := foundryObject(t, foundryList(t, data["sessions"])[0])
	sessionID, revision := conversation["id"].(string), conversation["revision"].(string)
	type answer struct {
		status int
		body   map[string]any
	}
	// Several new ranges, each one asked by two callers at once: the second one is sent a beat
	// later, which is the window two tabs really have when they submit the same range.
	// Each range starts on a human request of the fixture and none of them overlaps another.
	for _, range_ := range [][2]int{{0, 0}, {2, 2}, {4, 4}, {6, 6}, {8, 8}, {10, 10}, {12, 12}, {14, 14}, {16, 16}, {18, 18}} {
		request := map[string]any{"session_id": sessionID, "revision": revision, "first_event": range_[0], "last_event": range_[1], "title": fmt.Sprintf("rango concurrente %d-%d", range_[0], range_[1])}
		answers := make([]answer, 2)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for index := range answers {
			wg.Add(1)
			go func(slot int) {
				defer wg.Done()
				<-start
				status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/tasks", request)
				answers[slot] = answer{status: status, body: body}
			}(index)
		}
		close(start)
		wg.Wait()

		created, reused := 0, 0
		ids := map[string]bool{}
		for _, item := range answers {
			task, ok := item.body["task"].(map[string]any)
			if !ok {
				t.Fatalf("concurrent create of %v answered %d without a task: %+v", range_, item.status, item.body)
			}
			ids[task["id"].(string)] = true
			switch {
			case item.status == http.StatusCreated && item.body["reused"] == false:
				created++
			case item.status == http.StatusOK && item.body["reused"] == true:
				reused++
			default:
				t.Fatalf("concurrent create of %v answered status=%d body=%+v", range_, item.status, item.body)
			}
			wanted := request["first_event"].(int)
			if task["first_event"] != float64(wanted) || task["last_event"] != float64(range_[1]) {
				t.Fatalf("concurrent create of %v kept another range: %+v", range_, task)
			}
		}
		if created != 1 || reused != 1 || len(ids) != 1 {
			t.Fatalf("concurrent create of %v created=%d reused=%d ids=%v, want 1/1/same", range_, created, reused, ids)
		}
	}
	// The same range again, now sequential: it is the stored task, not a second row.
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks", map[string]any{"session_id": sessionID, "revision": revision, "first_event": 0, "last_event": 0, "title": "rango nuevo concurrente"})
	if status != http.StatusOK || data["reused"] != true {
		t.Fatalf("repeating the range status=%d body=%+v", status, data)
	}
	status, data = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/tasks?session_id="+sessionID, nil)
	if status != http.StatusOK || len(foundryList(t, data["tasks"])) != 10 {
		t.Fatalf("every range must hold exactly one task: status=%d body=%+v", status, data)
	}
	// A real overlap is still refused.
	status, data = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks", map[string]any{"session_id": sessionID, "revision": revision, "first_event": 0, "last_event": 1, "title": "solape concurrente"})
	if status != http.StatusConflict {
		t.Fatalf("overlapping range expected 409, observed status=%d body=%+v", status, data)
	}
	foundryErrorCode(t, data, "task_range_overlap")
}

// The ingestion boundary is shared by registration, relink, discovery and the scan itself:
// credentials, keys, tool configuration and the OpenCode history store are never read, a
// direct root of that kind is refused before its content is opened, and a mixed parent tree
// keeps capturing the allowed files it contains.
func TestSourceTrustBoundaryKeepsSensitiveTreesOut(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "consented-boundary")
	canonicalDir := filepath.Join(sourceRoot, "allowed")
	sensitive := []struct {
		dir     string
		marker  string
		adapter string
	}{
		{filepath.Join(sourceRoot, "auth"), "CV01-FAKE-AUTH-CANARY", "canonical"},
		{filepath.Join(sourceRoot, "credentials"), "CV01-FAKE-CREDENTIALS-CANARY", "canonical"},
		{filepath.Join(sourceRoot, ".ssh"), "CV01-FAKE-SSH-CANARY", "canonical"},
	}
	for _, item := range sensitive {
		if err := os.MkdirAll(item.dir, 0700); err != nil {
			t.Fatal(err)
		}
		writeJSONLFixture(t, filepath.Join(item.dir, "session.jsonl"),
			map[string]any{"session_id": "canary-" + filepath.Base(item.dir), "role": "user", "kind": "message", "text": item.marker},
		)
	}
	if err := os.MkdirAll(canonicalDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(canonicalDir, "session.jsonl"),
		map[string]any{"session_id": "boundary-allowed", "role": "user", "kind": "message", "text": "conversación permitida del árbol mixto"},
	)
	// A legitimate banner that only shares a substring with the policy, and a tree that merely
	// lives under a `.config` directory, stay usable.
	allowed := []string{
		filepath.Join(sourceRoot, "auth-demo"),
		filepath.Join(root, ".config", "project-sessions"),
	}
	for _, dir := range allowed {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		writeJSONLFixture(t, filepath.Join(dir, "session.jsonl"),
			map[string]any{"session_id": "allowed-" + filepath.Base(dir), "role": "user", "kind": "message", "text": "conversación permitida " + filepath.Base(dir)},
		)
	}
	// The OpenCode history store of this release has no parser: it is reported, not ingested,
	// in its own `.opencode` folder and in the XDG layout that keeps it as share/opencode.
	opencode := filepath.Join(sourceRoot, ".opencode", "storage", "session", "project")
	xdgOpencode := filepath.Join(sourceRoot, ".local", "share", "opencode", "storage", "session", "project")
	for _, dir := range []string{opencode, xdgOpencode} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		writeJSONLFixture(t, filepath.Join(dir, "session.json"),
			map[string]any{"session_id": "opencode-canary", "role": "user", "kind": "message", "text": "CV02-FAKE-OPENCODE-CANARY"},
		)
	}
	// A project folder that merely carries the word is not the history store.
	projectOpencode := filepath.Join(sourceRoot, "projects", "opencode")
	if err := os.MkdirAll(projectOpencode, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(projectOpencode, "session.jsonl"),
		map[string]any{"session_id": "project-opencode", "role": "user", "kind": "message", "text": "conversación de un proyecto llamado opencode"},
	)

	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	client := server.Client()
	listSources := func() []any {
		t.Helper()
		status, body := callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations/sources", nil)
		if status != http.StatusOK {
			t.Fatalf("list sources status=%d body=%+v", status, body)
		}
		return foundryList(t, body["sources"])
	}

	// A direct sensitive root is refused with its own reason, before anything is read.
	for _, item := range sensitive {
		status, body := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": item.dir, "adapter": item.adapter})
		if status != http.StatusBadRequest {
			t.Fatalf("registering %s expected 400, observed status=%d body=%+v", item.dir, status, body)
		}
		foundryErrorCode(t, body, "sensitive_source")
	}
	for _, store := range []string{filepath.Join(sourceRoot, ".opencode"), filepath.Join(sourceRoot, ".local", "share", "opencode", "storage")} {
		status, body := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": store, "adapter": "canonical"})
		if status != http.StatusBadRequest {
			t.Fatalf("registering %s expected 400, observed status=%d body=%+v", store, status, body)
		}
		foundryErrorCode(t, body, "opencode_history_not_supported")
	}
	if len(listSources()) != 0 {
		t.Fatalf("a refused root must not be registered: %+v", listSources())
	}
	// A project folder that merely carries that word stays usable, like any other tree.
	status, body := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": projectOpencode, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("a project folder named opencode must stay usable: status=%d body=%+v", status, body)
	}
	if len(listSources()) != 1 {
		t.Fatalf("the allowed project folder is the only registered source: %+v", listSources())
	}

	// The mixed parent tree is captured without its sensitive subtrees, and the subtree is
	// never read: its own permissions do not even allow listing it.
	for _, item := range sensitive {
		if err := os.Chmod(item.dir, 0000); err != nil {
			t.Fatal(err)
		}
	}
	opencodeMode := filepath.Join(sourceRoot, ".opencode")
	xdgOpencodeMode := filepath.Join(sourceRoot, ".local", "share", "opencode")
	for _, dir := range []string{opencodeMode, xdgOpencodeMode} {
		if err := os.Chmod(dir, 0000); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, item := range sensitive {
			_ = os.Chmod(item.dir, 0700)
		}
		_ = os.Chmod(opencodeMode, 0700)
		_ = os.Chmod(xdgOpencodeMode, 0700)
	})
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("registering the mixed tree status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)
	job := queueFoundryScan(t, client, server.URL, sourceID)
	if job["status"] != "succeeded" {
		t.Fatalf("scan of the mixed tree: %+v", job)
	}
	progress := foundryObject(t, job["progress"])
	if progress["files"] != float64(3) || progress["errors"] != float64(0) {
		t.Fatalf("the allowed files are the only ones captured: %+v", progress)
	}
	status, body = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	if status != http.StatusOK {
		t.Fatalf("list conversations status=%d body=%+v", status, body)
	}
	if captured := foundryList(t, body["sessions"]); len(captured) != 3 {
		t.Fatalf("the mixed tree captured %d conversations, want the three allowed ones: %+v", len(captured), captured)
	}
	for _, marker := range []string{"CV01-FAKE-AUTH-CANARY", "CV01-FAKE-CREDENTIALS-CANARY", "CV01-FAKE-SSH-CANARY", "CV02-FAKE-OPENCODE-CANARY"} {
		status, body = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations?q="+url.QueryEscape(marker), nil)
		if status != http.StatusOK || len(foundryList(t, body["sessions"])) != 0 {
			t.Fatalf("marker %s was ingested: status=%d body=%+v", marker, status, body)
		}
	}
	stored := map[string]any{}
	for _, raw := range listSources() {
		candidate := foundryObject(t, raw)
		if candidate["id"] == sourceID {
			stored = candidate
		}
	}
	coverage := foundryObject(t, stored["coverage"])
	if stored["last_scan"].(map[string]any)["errors"] != float64(0) || coverage["blocking_issue_count"] != float64(0) {
		t.Fatalf("an excluded subtree is not a capture error: %+v", stored)
	}
	if coverage["semantic_issue_count"] == float64(0) {
		t.Fatalf("the exclusion must stay visible in the coverage: %+v", coverage)
	}

	// Discovery reports the OpenCode store as unsupported and never offers it as a candidate.
	registeredBefore := len(listSources())
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources/discover", map[string]any{"roots": []any{
		map[string]any{"path": canonicalDir, "adapter": "canonical"},
		map[string]any{"path": filepath.Join(sourceRoot, ".opencode"), "adapter": "canonical"},
	}})
	if status != http.StatusOK {
		t.Fatalf("discover status=%d body=%+v", status, body)
	}
	candidates := foundryList(t, body["candidates"])
	if len(candidates) != 1 {
		t.Fatalf("discover offered %d candidates, want one: %+v", len(candidates), candidates)
	}
	offered := foundryObject(t, candidates[0])
	if offered["path"] != canonicalDir || offered["file_count"] != float64(1) {
		t.Fatalf("discover offered another tree: %+v", offered)
	}
	unsupported := foundryList(t, body["unsupported"])
	if len(unsupported) != 1 {
		t.Fatalf("discover must state the OpenCode store as unsupported: %+v", unsupported)
	}
	refused := foundryObject(t, unsupported[0])
	// The root was refused by the boundary itself, so it is reported as a path with the reason
	// that names the unsupported history.
	if refused["reason"] != "opencode_history_not_supported" || refused["kind"] != "path" {
		t.Fatalf("the OpenCode store reason is not useful: %+v", refused)
	}
	if len(listSources()) != registeredBefore {
		t.Fatalf("discovery must not register sources: %+v", listSources())
	}

	// The mixed parent is still offered, with its allowed files, and the excluded OpenCode
	// subtree is stated as the reason of an issue instead of hiding the whole root.
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources/discover", map[string]any{"roots": []any{
		map[string]any{"path": sourceRoot, "adapter": "canonical"},
	}})
	if status != http.StatusOK {
		t.Fatalf("discover of the mixed tree status=%d body=%+v", status, body)
	}
	offered = foundryObject(t, foundryList(t, body["candidates"])[0])
	if offered["path"] != sourceRoot || offered["file_count"] != float64(3) {
		t.Fatalf("the mixed tree must keep its allowed files: %+v", offered)
	}
	reasons := map[string]bool{}
	for _, raw := range foundryList(t, body["issues"]) {
		reasons[foundryObject(t, raw)["reason"].(string)] = true
	}
	if !reasons["opencode_history_not_supported"] || !reasons["sensitive_path_excluded"] {
		t.Fatalf("the exclusions must be stated with their reason: %+v", reasons)
	}
}

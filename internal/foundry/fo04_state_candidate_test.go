// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Focused FO-04 regression: with one prepared candidate row the learning state
// endpoint has to answer. Before the fix, listLearningCandidates ran a nested query
// (loadLearningTask over m.db) while its own rows were still open, and the single
// pooled connection (SetMaxOpenConns(1)) turned that into a deadlocked request, so the
// browser never reached the candidate screen that downloads the package.
func TestLearningStateAnswersWithAPreparedCandidate(t *testing.T) {
	root := t.TempDir()
	m, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	client := server.Client()
	sourceRoot := filepath.Join(root, "conversations")
	if err := os.MkdirAll(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "session.jsonl"),
		map[string]any{"session_id": "state-source", "role": "user", "kind": "message", "text": "A prepared candidate must not block the learning state."})
	status, body := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)
	if job := queueFoundryScan(t, client, server.URL, sourceID); job["status"] != "succeeded" {
		t.Fatalf("scan job=%+v", job)
	}
	status, body = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	if status != http.StatusOK {
		t.Fatalf("list conversations status=%d body=%+v", status, body)
	}
	session := foundryObject(t, foundryList(t, body["sessions"])[0])
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks", map[string]any{"session_id": session["id"], "revision": session["revision"], "first_event": 0, "last_event": 0, "title": "Prepared candidate state", "citations": []any{map[string]any{"event_index": 0, "quote": "A prepared candidate must not block"}}})
	if status != http.StatusCreated {
		t.Fatalf("create task status=%d body=%+v", status, body)
	}
	taskID := foundryObject(t, body["task"])["id"].(string)
	if status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+taskID+"/completion", map[string]any{"completed": true, "expected_generation": 0}); status != http.StatusOK {
		t.Fatalf("complete task status=%d body=%+v", status, body)
	}
	if status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+taskID+"/label", map[string]any{"label": "correct", "feedback": "Reviewed", "expected_generation": 1}); status != http.StatusOK {
		t.Fatalf("label task status=%d body=%+v", status, body)
	}
	spec := map[string]any{"name": "prepared-candidate", "description": "Prepared fixture", "kind": "rule", "scope": "project", "domain": "writing",
		"positive":   map[string]any{"instructions": "Keep the requirement.", "files": []any{map[string]any{"path": "notes/one.md", "content": "one"}}},
		"preventive": map[string]any{"instructions": "Do not generalise.", "files": []any{}},
		"evidence":   []any{map[string]any{"event_index": 0, "quote": "A prepared candidate must not block"}}, "replaces": []any{}, "rationale": "Fixture"}
	if _, err := m.db.Exec(`INSERT INTO candidates(id,task_id,spec_json,status,base_version,created) VALUES('candidate-state-fixture',?,?,'proposed','initial',?)`, taskID, mustJSON(spec), nowSeconds()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/foundry/learning/state?compact=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("the state endpoint did not answer with a prepared candidate: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("state status=%d", response.StatusCode)
	}
	var state map[string]any
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	candidates := foundryList(t, state["candidates"])
	if len(candidates) != 1 || foundryObject(t, candidates[0])["id"] != "candidate-state-fixture" {
		t.Fatalf("state did not publish the prepared candidate: %+v", state["candidates"])
	}
}

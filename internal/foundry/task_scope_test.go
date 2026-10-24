package foundry

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// Label and feedback decisions obey the same host scope as completion: a task whose
// host left the current scope reads as not found and is left untouched.
func TestTaskDecisionsRespectHostScope(t *testing.T) {
	root := t.TempDir()
	m, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	client := server.Client()
	sourceRoot := filepath.Join(root, "conversations")
	if err := os.MkdirAll(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "session.jsonl"),
		map[string]any{"session_id": "scope-source", "role": "user", "kind": "message", "text": "Scope check."})
	status, body := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)
	queueFoundryScan(t, client, server.URL, sourceID)
	_, body = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	session := foundryObject(t, foundryList(t, body["sessions"])[0])
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks", map[string]any{"session_id": session["id"], "revision": session["revision"], "first_event": 0, "last_event": 0, "title": "Scoped", "citations": []any{map[string]any{"event_index": 0, "quote": "Scope check."}}})
	if status != http.StatusCreated {
		t.Fatalf("create task status=%d body=%+v", status, body)
	}
	taskID := foundryObject(t, body["task"])["id"].(string)
	if status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+taskID+"/completion", map[string]any{"completed": true, "expected_generation": 0}); status != http.StatusOK {
		t.Fatalf("complete status=%d body=%+v", status, body)
	}
	if _, err := m.db.Exec(`UPDATE sessions SET host_id='host-outside-scope' WHERE id=?`, session["id"]); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		path    string
		payload map[string]any
	}{
		{"/label", map[string]any{"label": "correct"}},
		{"/feedback", map[string]any{"feedback": "note"}},
		{"/completion", map[string]any{"completed": false}},
	} {
		if status, body := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+taskID+call.path, call.payload); status != http.StatusNotFound {
			t.Fatalf("%s outside the host scope: status=%d body=%+v", call.path, status, body)
		}
	}
	var label string
	if err := m.db.QueryRow(`SELECT label FROM tasks WHERE id=?`, taskID).Scan(&label); err != nil || label != "pending" {
		t.Fatalf("label=%q err=%v", label, err)
	}
}

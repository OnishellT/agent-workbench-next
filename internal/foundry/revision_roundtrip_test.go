package foundry

import (
	"net/http"
	"path/filepath"
	"testing"
)

// A source file that goes A -> B -> A must not hit the (session_id, revision) primary
// key: the old revision is reused and the session is repointed at it.
func TestRevisionRoundTripABAReusesExistingRevision(t *testing.T) {
	root := t.TempDir()
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	path := filepath.Join(root, "aba.jsonl")
	write := func(text string) {
		writeJSONLFixture(t, path,
			map[string]any{"type": "session_meta", "payload": map[string]any{"id": "aba-session", "cwd": "/aba", "version": "v1", "source": "fixture"}},
			map[string]any{"type": "user", "uuid": "aba-u", "sessionId": "aba-session", "timestamp": "2026-10-17T10:00:00Z", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}},
		)
	}
	write("version A")
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": path, "adapter": "claude"})
	if status != http.StatusCreated {
		t.Fatalf("register status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)
	scan := func() string {
		job := queueFoundryScan(t, server.Client(), server.URL, sourceID)
		if job["status"] != "succeeded" {
			t.Fatalf("scan failed: %+v", job)
		}
		sessions, revision := sourceConversations(t, server, sourceID)
		if len(sessions) != 1 {
			t.Fatalf("sessions=%d", len(sessions))
		}
		return revision
	}
	a := scan()
	write("version B")
	b := scan()
	write("version A")
	again := scan()
	if a == b || again != a {
		t.Fatalf("revisions a=%q b=%q again=%q: A->B->A must land on A", a, b, again)
	}
}

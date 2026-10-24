// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaskConfigAndProposalRoutes(t *testing.T) {
	module, err := New(Options{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(module.Handler())
	defer server.Close()
	defer func() {
		if err := module.Close(t.Context()); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/tasks/config", nil)
	if status != http.StatusOK {
		t.Fatalf("GET task config status=%d body=%+v", status, body)
	}
	config := foundryObject(t, body["config"])
	if config["auto_tasks"] != false || config["task_idle_seconds"] != float64(defaultTaskIdleSeconds) {
		t.Fatalf("unexpected task defaults: %+v", config)
	}
	provider := foundryObject(t, foundryObject(t, body["diagnostics"])["provider"])
	if provider["available"] != false {
		t.Fatalf("default Pi without an explicit model should fail closed: %+v", provider)
	}

	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/tasks/config", map[string]any{"config": map[string]any{"task_idle_seconds": 240, "auto_tasks": true}})
	if status != http.StatusOK {
		t.Fatalf("PATCH task config status=%d body=%+v", status, body)
	}
	config = foundryObject(t, body["config"])
	if config["auto_tasks"] != true || config["task_idle_seconds"] != float64(240) {
		t.Fatalf("config patch not reflected: %+v", config)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/tasks/config", map[string]any{"config": map[string]any{"not_allowed": true}})
	if status != http.StatusBadRequest {
		t.Fatalf("unknown config field expected 400, got %d body=%+v", status, body)
	}
	foundryErrorCode(t, body, "invalid_config")

	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/tasks/proposals", map[string]any{"session_id": "not-captured", "expected_revision": "rev0"})
	if status != http.StatusGone {
		t.Fatalf("legacy proposal route expected 410, got %d body=%+v", status, body)
	}
	foundryErrorCode(t, body, "legacy_inference_disabled")
	if _, apiErr := module.enqueueTaskProposal(t.Context(), "not-captured", "rev0", false); apiErr == nil || apiErr.Code != "session_not_found" {
		t.Fatalf("internal proposal gate = %+v", apiErr)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/tasks/proposals/not-captured?revision=rev0", nil)
	if status != http.StatusNotFound {
		t.Fatalf("proposal lookup expected 404, got %d body=%+v", status, body)
	}
	foundryErrorCode(t, body, "proposal_not_found")
}

func TestProposalChunkerPreservesAllTextAndMetadataWithinLimit(t *testing.T) {
	event := proposalEvent{EventIndex: 0, ID: "evt0", Role: "user", Kind: "message", Text: strings.Repeat("🙂alpha-日本語\n", 90), Timestamp: "2026-01-01T00:00:00Z", Attachments: []any{map[string]any{"name": "fixture.txt"}}, Inert: true}
	limit := 1000
	chunks, receipts, err := chunkProposalEvidence([]proposalEvent{event}, limit)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for long fixture; got %d", len(chunks))
	}
	if len(receipts) != 1 || receipts[0]["event_index"] != 0 {
		t.Fatalf("unexpected event coverage receipt: %+v", receipts)
	}
	metadata := mustJSON(map[string]any{"event_index": event.EventIndex, "id": event.ID, "role": event.Role, "kind": event.Kind, "timestamp": event.Timestamp, "channel": event.Channel, "tool_name": event.ToolName, "call_id": event.CallID, "attachments": event.Attachments, "inert": event.Inert})
	expected := map[string][]rune{"metadata": []rune(metadata), "text": []rune(event.Text)}
	covered := map[string]map[int]int{"metadata": {}, "text": {}}
	for chunkIndex, chunk := range chunks {
		if got := runeCount(chunk.Prompt); got > limit {
			t.Fatalf("chunk %d prompt chars=%d exceeds max_input_chars=%d", chunkIndex, got, limit)
		}
		for _, fragment := range chunk.Fragments {
			part, ok := fragment["part"].(string)
			if !ok {
				t.Fatalf("chunk fragment missing part: %+v", fragment)
			}
			start, okStart := fragment["start"].(int)
			end, okEnd := fragment["end"].(int)
			if !okStart || !okEnd || start < 0 || end < start || end > len(expected[part]) {
				t.Fatalf("invalid fragment range: %+v", fragment)
			}
			field := "data"
			if part == "text" {
				field = "text"
			}
			value, _ := fragment[field].(string)
			gotRunes := []rune(value)
			if len(gotRunes) != end-start || string(gotRunes) != string(expected[part][start:end]) {
				t.Fatalf("fragment does not preserve original %s range %+v", part, fragment)
			}
			for position := start; position < end; position++ {
				covered[part][position]++
			}
		}
	}
	for part, runes := range expected {
		if len(covered[part]) != len(runes) {
			t.Fatalf("coverage for %s is %d runes, want %d", part, len(covered[part]), len(runes))
		}
		for position := range runes {
			if covered[part][position] != 1 {
				t.Fatalf("%s rune %d covered %d times", part, position, covered[part][position])
			}
		}
	}
}

func TestTaskSuggestionValidationRejectsUnsupportedAndOverlappingEvidence(t *testing.T) {
	events := []proposalEvent{{EventIndex: 0, Role: "user", Text: "Please create a report."}, {EventIndex: 1, Role: "assistant", Text: "Report is drafted."}}
	valid := TaskSuggestion{Title: "Create a report", FirstEvent: 0, LastEvent: 1, Evidence: []TaskCitation{{EventIndex: 0, Quote: "Please create a report"}}}
	if err := validateTaskSuggestions([]TaskSuggestion{valid}, events, nil); err != nil {
		t.Fatalf("valid evidence rejected: %v", err)
	}
	invalid := valid
	invalid.Evidence = []TaskCitation{{EventIndex: 0, Quote: "not in transcript"}}
	if err := validateTaskSuggestions([]TaskSuggestion{invalid}, events, nil); err == nil {
		t.Fatal("unsupported citation was accepted")
	}
	second := TaskSuggestion{Title: "Follow-up", FirstEvent: 1, LastEvent: 1, Evidence: []TaskCitation{{EventIndex: 1, Quote: "Report is drafted"}}}
	if err := validateTaskSuggestions([]TaskSuggestion{valid, second}, events, nil); err == nil {
		t.Fatal("overlapping task ranges were accepted")
	}
}

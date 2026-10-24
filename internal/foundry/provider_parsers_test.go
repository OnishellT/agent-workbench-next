// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"bytes"
	"encoding/json"
	"testing"
)

func providerJSONL(events ...map[string]any) []byte {
	var output bytes.Buffer
	for _, event := range events {
		encoded, _ := json.Marshal(event)
		output.Write(encoded)
		output.WriteByte('\n')
	}
	return output.Bytes()
}

func TestPiAnalysisParserRequiresSettledNoToolsAndSchemaValidJSON(t *testing.T) {
	message := map[string]any{"role": "assistant", "stopReason": "stop", "responseModel": "fixture-model", "provider": "fixture", "content": []any{map[string]any{"type": "text", "text": `{"observations":[]}`}}, "usage": map[string]any{"input": 4, "output": 2, "totalTokens": 6}}
	valid := providerJSONL(map[string]any{"type": "message_end", "message": message}, map[string]any{"type": "agent_settled", "aborted": false})
	parsed, err := parsePiResponse(valid, taskObservationSchema)
	if err != nil {
		t.Fatalf("valid settled no-tools response rejected: %v", err)
	}
	if parsed.text != `{"observations":[]}` || parsed.model != "fixture-model" || parsed.provider != "fixture" || parsed.toolCalls == nil || *parsed.toolCalls != 0 {
		t.Fatalf("unexpected Pi response receipt: %+v", parsed)
	}
	toolUse := providerJSONL(map[string]any{"type": "tool_execution_start"}, map[string]any{"type": "message_end", "message": message}, map[string]any{"type": "agent_settled", "aborted": false})
	if _, err := parsePiResponse(toolUse, taskObservationSchema); err == nil {
		t.Fatal("analysis accepted a response containing a tool call")
	}
	if _, err := parsePiResponse(providerJSONL(map[string]any{"type": "message_end", "message": message}), taskObservationSchema); err == nil {
		t.Fatal("analysis accepted a response without agent_settled")
	}
	badJSON := map[string]any{"role": "assistant", "stopReason": "stop", "responseModel": "fixture-model", "provider": "fixture", "content": []any{map[string]any{"type": "text", "text": `{"observations":[]} trailing`}}}
	if _, err := parsePiResponse(providerJSONL(map[string]any{"type": "message_end", "message": badJSON}, map[string]any{"type": "agent_settled", "aborted": false}), taskObservationSchema); err == nil {
		t.Fatal("analysis accepted extra text after JSON")
	}
}

func TestCodexParserReportsReplayTurnWithoutClaimingToolFreeAnalysis(t *testing.T) {
	output := providerJSONL(
		map[string]any{"type": "turn.completed", "model": "fixture-codex", "usage": map[string]any{"input_tokens": 9, "output_tokens": 4}},
		map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": "replay output"}},
		map[string]any{"type": "item.completed", "item": map[string]any{"type": "command_execution"}},
	)
	parsed, err := parseCodexResponse(output)
	if err != nil {
		t.Fatalf("valid replay response rejected: %v", err)
	}
	if parsed.model != "fixture-codex" || parsed.text != "replay output" || parsed.turns == nil || *parsed.turns != 1 || parsed.toolCalls == nil || *parsed.toolCalls != 1 {
		t.Fatalf("unexpected replay receipt: %+v", parsed)
	}
	if _, err := parseCodexResponse(providerJSONL(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": "no turn"}})); err == nil {
		t.Fatal("Codex response without a completed turn was accepted")
	}
}

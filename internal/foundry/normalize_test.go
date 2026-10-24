// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeAdaptersShareTypedInertEvents(t *testing.T) {
	cases := []struct {
		name              string
		adapter           Adapter
		input, role, text string
	}{
		{"codex", AdapterCodex, "{\"type\":\"item_completed\",\"payload\":{\"item\":{\"id\":\"m1\",\"type\":\"AgentMessage\",\"text\":\"codex reply\",\"channel\":\"final\"}}}", "assistant", "codex reply"},
		{"claude", AdapterClaude, "{\"type\":\"assistant\",\"message\":\"claude reply\"}", "assistant", "claude reply"},
		{"pi", AdapterPi, "{\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"pi reply\"}]}}", "assistant", "pi reply"},
		{"omp", AdapterOMP, "{\"role\":\"assistant\",\"kind\":\"message\",\"text\":\"omp reply\",\"host_id\":\"local\"}", "assistant", "omp reply"},
		{"canonical", AdapterCanonical, "{\"role\":\"assistant\",\"kind\":\"message\",\"text\":\"canonical reply\",\"host_id\":\"local\"}", "assistant", "canonical reply"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transcript, err := NormalizeJSONL(strings.NewReader(tc.input+"\n"), NormalizeOptions{Adapter: tc.adapter, FallbackSessionID: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			if len(transcript.Events) != 1 {
				t.Fatalf("events: got %d want 1", len(transcript.Events))
			}
			event := transcript.Events[0]
			if event["role"] != tc.role || event["text"] != tc.text {
				t.Fatalf("normalized role/text = %v/%v, want %q/%q", event["role"], event["text"], tc.role, tc.text)
			}
			if event["inert"] != true {
				t.Fatalf("event must remain inert: %#v", event["inert"])
			}
			if transcript.Coverage.Status != "complete" {
				t.Fatalf("coverage = %q, want complete", transcript.Coverage.Status)
			}
		})
	}
}

func TestNormalizeCanonicalHostAndRedactsTextAndMetadata(t *testing.T) {
	secret := "sk-proj-abcdefghijk"
	input := "{\"session_id\":\"s1\",\"role\":\"assistant\",\"text\":\"use " + secret + "\",\"host_id\":\"local\",\"api_key\":\"value-that-must-not-leak\"}"
	transcript, err := NormalizeJSONL(strings.NewReader(input+"\n"), NormalizeOptions{Adapter: AdapterCanonical, FallbackSessionID: "fallback"})
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Events) != 1 {
		t.Fatalf("events: got %d want 1", len(transcript.Events))
	}
	event := transcript.Events[0]
	if event["host_id"] != "local" {
		t.Fatalf("host_id = %v", event["host_id"])
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(secret)) || bytes.Contains(encoded, []byte("value-that-must-not-leak")) {
		t.Fatalf("secret survived normalization: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte("REDACTED")) {
		t.Fatalf("redaction marker missing: %s", encoded)
	}
	wantHash := sha256.Sum256([]byte(input + "\n"))
	if transcript.Coverage.RawSHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("raw sha = %s", transcript.Coverage.RawSHA256)
	}
}

func TestNormalizeUnknownRecordsArePreservedWithSemanticIssue(t *testing.T) {
	transcript, err := NormalizeJSONL(strings.NewReader("{\"session_id\":\"s1\",\"type\":\"future_event\",\"payload\":{\"detail\":\"safe\"}}"+"\n"), NormalizeOptions{Adapter: AdapterCanonical, FallbackSessionID: "fallback"})
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Events) != 1 || transcript.Coverage.IssueCount != 1 || transcript.Coverage.SemanticIssueCount != 1 || transcript.Coverage.BlockingIssueCount != 0 {
		t.Fatalf("unknown record coverage mismatch: events=%d coverage=%+v", len(transcript.Events), transcript.Coverage)
	}
	if transcript.Coverage.Status != "complete" || transcript.Events[0]["kind"] != "native_event" || transcript.Events[0]["inert"] != true {
		t.Fatalf("unknown record was not inert semantic evidence: %+v", transcript.Events[0])
	}
	if transcript.Coverage.Issues[0].Retained != true {
		t.Fatalf("unknown raw record not marked retained: %+v", transcript.Coverage.Issues[0])
	}
}

func TestNormalizePartialJSONLDuplicateKeysAndLimits(t *testing.T) {
	partial := "{\"session_id\":\"s1\",\"role\":\"user\",\"text\":\"first\"}" + "\n" + "{\"session_id\":\"s1\",\"role\":\"assistant\",\"text\":\"unfinished\""
	transcript, err := NormalizeJSONL(strings.NewReader(partial), NormalizeOptions{Adapter: AdapterCanonical, FallbackSessionID: "fallback"})
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Events) != 1 || transcript.Coverage.Status != "partial" || !transcript.Coverage.AppendPending {
		t.Fatalf("partial JSONL lost state: events=%d coverage=%+v", len(transcript.Events), transcript.Coverage)
	}
	if len(transcript.Coverage.Issues) != 1 || transcript.Coverage.Issues[0].Reason != "partial_last_record" {
		t.Fatalf("partial issue = %+v", transcript.Coverage.Issues)
	}

	duplicate := "{\"role\":\"user\",\"role\":\"assistant\",\"text\":\"duplicate\"}" + "\n"
	parsedDuplicate, err := NormalizeJSONL(strings.NewReader(duplicate), NormalizeOptions{Adapter: AdapterCanonical, FallbackSessionID: "fallback"})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsedDuplicate.Events) != 0 || parsedDuplicate.Coverage.Issues[0].Reason != "malformed_json" || parsedDuplicate.Coverage.Status != "partial" {
		t.Fatalf("duplicate key accepted: %+v", parsedDuplicate)
	}

	if _, err := NormalizeJSONL(strings.NewReader("123456789"), NormalizeOptions{Adapter: AdapterCanonical, FallbackSessionID: "fallback", MaxBytes: 8}); !errors.Is(err, ErrSourceTooLarge) {
		t.Fatalf("byte cap expected ErrSourceTooLarge, got %v", err)
	}
	if _, err := NormalizeJSON(strings.NewReader("{}"), NormalizeOptions{Adapter: AdapterCanonical, FallbackSessionID: "fallback", MaxBytes: MaxSourceBytes + 1}); !errors.Is(err, ErrSourceTooLarge) {
		t.Fatalf("invalid configured cap expected ErrSourceTooLarge, got %v", err)
	}
	if _, err := NormalizeJSON(strings.NewReader("{}"), NormalizeOptions{Adapter: Adapter("unknown"), FallbackSessionID: "fallback"}); !errors.Is(err, ErrUnsupportedAdapter) {
		t.Fatalf("unsupported adapter expected ErrUnsupportedAdapter, got %v", err)
	}
}

func TestNormalizeIssueCountNotTruncatedWithIssueSample(t *testing.T) {
	var input strings.Builder
	for i := 0; i < 205; i++ {
		fmt.Fprintf(&input, "{\"type\":\"future_%d\"}\n", i)
	}
	transcript, err := NormalizeJSONL(strings.NewReader(input.String()), NormalizeOptions{Adapter: AdapterCanonical, FallbackSessionID: "fallback"})
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Coverage.IssueCount != 205 || transcript.Coverage.SemanticIssueCount != 205 || len(transcript.Coverage.Issues) != 200 {
		t.Fatalf("issue sampling/count mismatch: %+v", transcript.Coverage)
	}
}

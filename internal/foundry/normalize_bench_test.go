// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// benchTranscript builds a Claude-style JSONL transcript with prose, tool calls
// and opaque token-like strings so every redaction pattern is exercised.
func benchTranscript(records int) []byte {
	var out bytes.Buffer
	prose := strings.Repeat("The quick brown fox explains how the build pipeline works. ", 12)
	for i := 0; i < records; i++ {
		switch i % 3 {
		case 0:
			fmt.Fprintf(&out, `{"type":"user","uuid":"u%d","sessionId":"s","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":%q}}`+"\n", i, prose)
		case 1:
			fmt.Fprintf(&out, `{"type":"assistant","uuid":"a%d","sessionId":"s","message":{"role":"assistant","content":[{"type":"text","text":%q},{"type":"tool_use","id":"t%d","name":"Bash","input":{"command":"echo QWxhZGRpbjpvcGVuIHNlc2FtZQpBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWjAxMjM0NTY3ODk token=abc123"}}]}}`+"\n", i, prose, i)
		default:
			fmt.Fprintf(&out, `{"type":"user","uuid":"r%d","sessionId":"s","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t%d","content":%q}]}}`+"\n", i, i-1, prose)
		}
	}
	return out.Bytes()
}

func BenchmarkNormalizeJSONL(b *testing.B) {
	data := benchTranscript(600)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tr, err := NormalizeJSONL(bytes.NewReader(data), NormalizeOptions{Adapter: AdapterClaude, FallbackSessionID: "fallback"})
		if err != nil || len(tr.Events) == 0 {
			b.Fatal(err, len(tr.Events))
		}
	}
}

func BenchmarkRedactString(b *testing.B) {
	text := strings.Repeat("The quick brown fox explains how the build pipeline works. ", 12) + " QWxhZGRpbjpvcGVuIHNlc2FtZQpBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWjAxMjM0NTY3ODk"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := redactString(text, true); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTruncateRunes(b *testing.B) {
	text := strings.Repeat("abc ", 1500)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = truncateRunes(text, 8000)
	}
}

func BenchmarkInsertEvents(b *testing.B) {
	module, err := New(Options{StateDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	defer module.Close(context.Background())
	tr, err := NormalizeJSONL(bytes.NewReader(benchTranscript(300)), NormalizeOptions{Adapter: AdapterClaude, FallbackSessionID: "fallback"})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, err := module.db.BeginTx(context.Background(), nil)
		if err != nil {
			b.Fatal(err)
		}
		// events has no FK to sessions in the benchmark path we exercise only the insert cost.
		if _, err := tx.Exec(`PRAGMA defer_foreign_keys=ON`); err != nil {
			b.Fatal(err)
		}
		if err := insertEvents(context.Background(), tx, "s", "r", tr.Events); err != nil {
			b.Fatal(err)
		}
		tx.Rollback()
	}
	b.ReportMetric(float64(len(tr.Events)), "events/op")
}

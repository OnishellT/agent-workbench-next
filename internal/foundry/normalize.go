// SPDX-License-Identifier: AGPL-3.0-only
// Conversation Foundry normalization port. Transcript records are inert evidence.
package foundry

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxSourceBytes    int64 = 64 << 20
	NormalizerVersion       = "foundry-conversation-normalizer-v1"
	RedactionVersion        = "foundry-redact-v1"
	maxJSONNodes            = 20_000
	maxJSONDepth            = 20
	maxStringRunes          = 1_048_576
	maxCaptureIssues        = 200
)

type Adapter string

const (
	AdapterCodex     Adapter = "codex"
	AdapterClaude    Adapter = "claude"
	AdapterPi        Adapter = "pi"
	AdapterOMP       Adapter = "omp"
	AdapterCanonical Adapter = "canonical"
)

type Event map[string]any

type Issue struct {
	Line     int    `json:"line"`
	FileName string `json:"file_name,omitempty"`
	Reason   string `json:"reason"`
	Type     string `json:"type,omitempty"`
	Retained bool   `json:"retained"`
	Severity string `json:"severity,omitempty"`
}

type Coverage struct {
	Status             string  `json:"status"`
	Issues             []Issue `json:"issues"`
	IssueCount         int     `json:"issue_count"`
	BlockingIssueCount int     `json:"blocking_issue_count"`
	SemanticIssueCount int     `json:"semantic_issue_count"`
	RecordCount        int     `json:"record_count"`
	EventCount         int     `json:"event_count"`
	NormalizerVersion  string  `json:"normalizer_version"`
	RawSHA256          string  `json:"raw_sha256"`
	Bytes              int64   `json:"bytes"`
	AppendPending      bool    `json:"append_pending,omitempty"`
}

type Transcript struct {
	Events   []Event  `json:"events"`
	Coverage Coverage `json:"coverage"`
}

type NormalizeOptions struct {
	Adapter           Adapter
	FallbackSessionID string
	MaxBytes          int64
}

var ErrUnsupportedAdapter = errors.New("unsupported conversation adapter")
var ErrSourceTooLarge = errors.New("source_byte_limit")

func normalizeOptions(options NormalizeOptions) (NormalizeOptions, error) {
	aliases := map[string]Adapter{"codex_jsonl": AdapterCodex, "codex-jsonl": AdapterCodex, "claude-code": AdapterClaude, "jsonl": AdapterCanonical}
	if alias, ok := aliases[strings.ToLower(string(options.Adapter))]; ok {
		options.Adapter = alias
	}
	if options.Adapter != AdapterCodex && options.Adapter != AdapterClaude && options.Adapter != AdapterPi && options.Adapter != AdapterOMP && options.Adapter != AdapterCanonical {
		return options, ErrUnsupportedAdapter
	}
	if strings.TrimSpace(options.FallbackSessionID) == "" || len(options.FallbackSessionID) > 512 || !utf8.ValidString(options.FallbackSessionID) {
		return options, errors.New("fallback session id is required")
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = MaxSourceBytes
	}
	if options.MaxBytes > MaxSourceBytes {
		return options, ErrSourceTooLarge
	}
	return options, nil
}

// NormalizeJSONL streams one bounded JSONL source through the shared adapter
// normalizer. The caller owns source identity, no-follow file opening, and
// before/after file-stat verification; this function owns parsing/redaction.
func NormalizeJSONL(reader io.Reader, options NormalizeOptions) (Transcript, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return Transcript{}, err
	}
	if reader == nil {
		return Transcript{}, errors.New("nil source reader")
	}
	limit := &io.LimitedReader{R: reader, N: options.MaxBytes + 1}
	buf := bufio.NewReaderSize(limit, 64*1024)
	builder := newEventBuilder(options)
	h := sha256.New()
	var total int64
	line := 0
	for {
		raw, readErr := buf.ReadBytes('\n')
		if len(raw) != 0 {
			total += int64(len(raw))
			h.Write(raw)
			if total > options.MaxBytes {
				return Transcript{}, ErrSourceTooLarge
			}
		}
		if len(raw) == 0 && readErr == io.EOF {
			break
		}
		line++
		if len(bytes.TrimSpace(raw)) != 0 {
			record, parseErr := decodeJSONObject(raw)
			if parseErr != nil {
				reason := "malformed_json"
				if readErr == io.EOF && !bytes.HasSuffix(raw, []byte{'\n'}) {
					reason = "partial_last_record"
					builder.appendPending = true
				}
				builder.issue(Issue{Line: line, Reason: reason, Retained: false})
			} else {
				builder.recordCount++
				builder.process(record, line)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Transcript{}, readErr
		}
	}
	if total > options.MaxBytes {
		return Transcript{}, ErrSourceTooLarge
	}
	return builder.finish(h, total), nil
}

// NormalizeJSON parses a bounded JSON conversation export or canonical
// {session_id,events:[...]} container using the same adapter implementation.
func NormalizeJSON(reader io.Reader, options NormalizeOptions) (Transcript, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return Transcript{}, err
	}
	if reader == nil {
		return Transcript{}, errors.New("nil source reader")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, options.MaxBytes+1))
	if err != nil {
		return Transcript{}, err
	}
	if int64(len(raw)) > options.MaxBytes {
		return Transcript{}, ErrSourceTooLarge
	}
	h := sha256.Sum256(raw)
	builder := newEventBuilder(options)
	value, parseErr := decodeJSON(raw)
	if parseErr != nil {
		builder.issue(Issue{Line: 1, Reason: "malformed_json", Retained: false})
		return builder.finishHash(hex.EncodeToString(h[:]), int64(len(raw))), nil
	}
	root, ok := value.(map[string]any)
	if !ok {
		builder.issue(Issue{Line: 1, Reason: "record_not_object", Retained: false})
		return builder.finishHash(hex.EncodeToString(h[:]), int64(len(raw))), nil
	}
	children, hasChildren := root["events"]
	if !hasChildren {
		children, hasChildren = root["messages"]
	}
	if hasChildren {
		list, ok := children.([]any)
		if !ok {
			builder.issue(Issue{Line: 1, Reason: "record_not_object", Retained: false})
		} else {
			for index, child := range list {
				line := index + 1
				item, ok := child.(map[string]any)
				if !ok {
					builder.issue(Issue{Line: line, Reason: "record_not_object", Retained: false})
					continue
				}
				copyRecord := cloneMap(item)
				if _, exists := copyRecord["session_id"]; !exists {
					if sid := firstValue(root["session_id"], root["id"]); sid != nil {
						copyRecord["session_id"] = sid
					}
				}
				builder.recordCount++
				builder.process(copyRecord, line)
			}
		}
	} else {
		builder.recordCount = 1
		builder.process(root, 1)
	}
	return builder.finishHash(hex.EncodeToString(h[:]), int64(len(raw))), nil
}

func (b *eventBuilder) finish(h hash.Hash, bytesRead int64) Transcript {
	return b.finishHash(hex.EncodeToString(h.Sum(nil)), bytesRead)
}
func (b *eventBuilder) finishHash(rawHash string, bytesRead int64) Transcript {
	blocking, semantic := b.blockingIssueCount, b.semanticIssueCount
	status := "complete"
	if blocking > 0 {
		status = "partial"
	}
	return Transcript{Events: b.events, Coverage: Coverage{Status: status, Issues: b.issues, IssueCount: b.issueCount, BlockingIssueCount: blocking, SemanticIssueCount: semantic, RecordCount: b.recordCount, EventCount: len(b.events), NormalizerVersion: NormalizerVersion, RawSHA256: rawHash, Bytes: bytesRead, AppendPending: b.appendPending}}
}

func decodeJSONObject(raw []byte) (map[string]any, error) {
	v, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("record_not_object")
	}
	return m, nil
}

func decodeJSON(raw []byte) (any, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("invalid_unicode")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	value, err := decodeJSONValue(decoder, 0, &nodes)
	if err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("trailing_json")
		}
		return nil, err
	}
	return value, nil
}

func decodeJSONValue(decoder *json.Decoder, depth int, nodes *int) (any, error) {
	*nodes++
	if depth > maxJSONDepth || *nodes > maxJSONNodes {
		return nil, errors.New("structure_limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); ok {
		switch delimiter {
		case '{':
			result := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("invalid_json_key")
				}
				if _, duplicate := result[key]; duplicate {
					return nil, errors.New("duplicate_json_key")
				}
				value, err := decodeJSONValue(decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				result[key] = value
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, errors.New("malformed_json")
			}
			return result, nil
		case '[':
			result := make([]any, 0)
			for decoder.More() {
				value, err := decodeJSONValue(decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				result = append(result, value)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, errors.New("malformed_json")
			}
			return result, nil
		default:
			return nil, errors.New("malformed_json")
		}
	}
	switch value := token.(type) {
	case json.Number:
		if _, err := value.Float64(); err != nil {
			return nil, errors.New("nonfinite_json")
		}
		return value, nil
	case string:
		if !utf8.ValidString(value) {
			return nil, errors.New("invalid_unicode")
		}
		return value, nil
	case bool, nil:
		return value, nil
	default:
		return nil, errors.New("malformed_json")
	}
}

type eventBuilder struct {
	options            NormalizeOptions
	events             []Event
	issues             []Issue
	recordCount        int
	context            map[string]any
	preserved          map[int]bool
	current            map[string]any
	issueCount         int
	blockingIssueCount int
	semanticIssueCount int
	appendPending      bool
}

func newEventBuilder(options NormalizeOptions) *eventBuilder {
	return &eventBuilder{options: options, events: []Event{}, issues: []Issue{}, context: map[string]any{}, preserved: map[int]bool{}}
}
func (b *eventBuilder) issue(issue Issue) {
	b.issueCount++
	if issue.Retained {
		b.semanticIssueCount++
	} else {
		b.blockingIssueCount++
	}
	if len(b.issues) < maxCaptureIssues {
		b.issues = append(b.issues, issue)
	}
}
func (b *eventBuilder) process(record map[string]any, line int) {
	b.current = record
	if sid := firstValue(record["session_id"], record["sessionId"]); sid != nil {
		b.context["session_id"] = sid
	}
	typ := stringValue(firstValue(record["type"], record["kind"]))
	if typ == "session_meta" || typ == "session" {
		meta, ok := record["payload"].(map[string]any)
		if !ok {
			meta = record
		}
		b.context["session_id"] = firstValue(meta["id"], meta["session_id"], any(b.options.FallbackSessionID))
		b.context["parent_session_id"] = firstValue(meta["parent_session_id"], meta["parentSessionId"])
		metadata := map[string]any{}
		for _, key := range []string{"cwd", "version", "source", "parent_session_id"} {
			if value, ok := meta[key]; ok {
				metadata[key] = value
			}
		}
		b.emit(meta, record, line, "service", "session_metadata", "", map[string]any{"metadata": metadata, "semantic_support": "specialized"})
		return
	}
	if b.options.Adapter == AdapterCodex {
		b.processCodex(record, line)
		return
	}
	b.processGeneric(record, line, typ)
}

func (b *eventBuilder) processCodex(record map[string]any, line int) {
	payload, ok := record["payload"].(map[string]any)
	if !ok {
		b.preserve(record, record, line, "unsupported_codex_event", stringValue(record["type"]))
		return
	}
	typ := stringValue(record["type"])
	item := stringValue(payload["type"])
	completed, completedOK := payload["item"].(map[string]any)
	if (typ == "item_completed" || item == "item_completed") && completedOK && (stringValue(completed["type"]) == "AgentMessage" || stringValue(completed["type"]) == "UserMessage") {
		role := "assistant"
		if stringValue(completed["type"]) == "UserMessage" {
			role = "user"
		}
		message := cloneMap(completed)
		message["role"] = role
		b.message(message, record, line)
		return
	}
	if item == "function_call" || item == "custom_tool_call" || item == "tool_call" || item == "function_call_output" || item == "custom_tool_call_output" || item == "tool_result" {
		isResult := strings.HasSuffix(item, "_output") || item == "tool_result"
		body := firstValue(payload["output"], payload["text"])
		kind := "tool_call"
		if isResult {
			kind = "tool_result"
			if body == nil {
				body = ""
			}
		} else {
			body = firstValue(payload["arguments"], payload["input"], any(""))
		}
		role := "tool"
		b.emit(payload, record, line, role, kind, printable(body), map[string]any{"tool_name": payload["name"], "call_id": payload["call_id"], "semantic_support": "specialized"})
		return
	}
	if typ == "response_item" && item == "message" {
		b.message(payload, record, line)
		return
	}
	if typ == "response_item" && item == "agent_message" {
		message := cloneMap(payload)
		role := stringValue(firstValue(payload["role"], payload["author"]))
		if !isRole(role) {
			role = "assistant"
		}
		message["role"] = role
		b.message(message, record, line)
		return
	}
	if item == "task_complete" || item == "turn_end" {
		b.emit(payload, record, line, "service", "completion", stringValue(payload["last_agent_message"]), map[string]any{"completion_suggested": true, "boundary": item})
		return
	}
	if typ == "event_msg" && (item == "user_message" || item == "agent_message") {
		body, attachments, tools, issues := content(firstValue(payload["message"], payload["content"], payload["text"]))
		for _, issue := range issues {
			issue.Line = line
			b.issue(issue)
		}
		role := "assistant"
		if item == "user_message" {
			role = "user"
		}
		b.emit(payload, record, line, role, "notification", body, map[string]any{"mirrored": true, "attachments": attachments, "content_tools": tools, "completion_suggested": firstString(payload["phase"], payload["channel"]) == "final" || firstString(payload["phase"], payload["channel"]) == "final_answer"})
		return
	}
	if typ == "turn_context" || containsString([]string{"token_count", "task_started", "agent_reasoning", "reasoning", "compaction", "context_compacted", "turn_aborted"}, item) {
		b.emit(payload, record, line, "service", "native_event", stringValue(payload["text"]), map[string]any{"native_type": firstString(item, typ)})
		return
	}
	b.preserve(payload, record, line, "unsupported_codex_event", firstString(item, typ))
}

func (b *eventBuilder) processGeneric(record map[string]any, line int, typ string) {
	if b.options.Adapter == AdapterCanonical && (typ == "orbit.incomplete-native-evidence" || typ == "orbit.redaction-limit") {
		b.emit(record, record, line, "service", "native_event", stringValue(record["text"]), map[string]any{"native_type": typ})
		b.issue(Issue{Line: line, Reason: "incomplete_source_evidence", Retained: false})
		return
	}
	if typ == "task_complete" || typ == "turn_end" || typ == "final" || stringValue(record["completion"]) == "task_complete" {
		b.emit(record, record, line, "service", "completion", stringValue(record["text"]), map[string]any{"completion_suggested": true, "boundary": typ})
		return
	}
	if message, ok := record["message"].(map[string]any); ok {
		b.message(message, record, line)
		return
	}
	if _, ok := record["role"]; ok {
		kind := stringValue(record["kind"])
		if kind == "tool_call" || kind == "tool_result" {
			b.emit(record, record, line, stringValue(record["role"]), kind, stringValue(record["text"]), map[string]any{"tool_name": record["tool_name"], "call_id": record["call_id"]})
			return
		}
		b.message(record, record, line)
		return
	}
	if containsString([]string{"assistant", "user", "system"}, typ) {
		if text, ok := record["message"].(string); ok {
			b.message(map[string]any{"role": typ, "content": text}, record, line)
			return
		}
	}
	if containsString([]string{"summary", "custom", "model_change", "thinking_level_change", "compaction", "branch_summary", "label", "session_info", "file-history-snapshot", "queue-operation"}, typ) {
		b.emit(record, record, line, "service", "native_event", stringValue(firstValue(record["summary"], record["text"])), map[string]any{"native_type": typ})
		return
	}
	b.preserve(record, record, line, "unsupported_record", typ)
}

// messageIdentity returns the record that owns the identity of a wrapped message: Claude and
// Pi keep the native uuid/sessionId outside `message`, so those identifiers win and only the
// keys the outer record does not carry are borrowed from the wrapped object.
func messageIdentity(record, message map[string]any) map[string]any {
	if len(record) == 0 || len(message) == 0 {
		return record
	}
	identity := record
	copied := false
	for _, key := range []string{"id", "message_id", "uuid", "session_id", "sessionId", "sequence", "ordinal", "timestamp", "parent_session_id", "parentId", "parentUuid", "branch_id"} {
		if _, ok := identity[key]; ok {
			continue
		}
		value, ok := message[key]
		if !ok {
			continue
		}
		if !copied {
			identity = cloneMap(record)
			copied = true
		}
		identity[key] = value
	}
	return identity
}

func (b *eventBuilder) message(message, raw map[string]any, line int) {
	role := stringValue(firstValue(message["role"], raw["role"], raw["type"]))
	if !isRole(role) {
		role = "service"
	}
	text, attachments, tools, issues := content(firstValue(message["content"], message["text"], raw["text"]))
	if rawAttachments, ok := raw["attachments"].([]any); ok {
		for _, attachment := range rawAttachments {
			attachments = append(attachments, descriptor(attachment))
		}
	}
	for _, issue := range issues {
		issue.Line = line
		b.issue(issue)
	}
	channel := firstValue(message["channel"], message["phase"], raw["channel"])
	if stringValue(channel) == "final_answer" {
		channel = "final"
	}
	if text != "" || len(attachments) > 0 || len(tools) == 0 {
		kind := "message"
		if role == "tool" || role == "toolResult" {
			kind = "tool_result"
			role = "tool"
		}
		b.emit(messageIdentity(raw, message), raw, line, role, kind, text, map[string]any{"attachments": attachments, "channel": channel, "completion_suggested": stringValue(channel) == "final", "call_id": firstValue(message["toolCallId"], message["call_id"]), "tool_name": message["toolName"], "semantic_support": "specialized"})
	}
	for index, tool := range tools {
		toolType := stringValue(tool["type"])
		isResult := toolType == "tool_result"
		kind := "tool_call"
		body := firstValue(tool["input"], tool["arguments"], any(""))
		if isResult {
			kind = "tool_result"
			body = firstValue(tool["content"], any(""))
		}
		toolRecord := cloneMap(raw)
		if id, ok := tool["id"]; ok {
			toolRecord["id"] = id
		}
		b.emit(toolRecord, raw, line, "tool", kind, printable(body), map[string]any{"tool_name": tool["name"], "call_id": firstValue(tool["tool_use_id"], tool["id"]), "content_part": index, "semantic_support": "specialized"})
	}
}

func (b *eventBuilder) preserve(record, raw map[string]any, line int, reason, nativeType string) {
	before := len(b.events)
	b.emit(record, raw, line, "service", "native_event", "", map[string]any{"native_type": nativeType, "semantic_support": "raw_only", "completion_suggested": false})
	if len(b.events) > before {
		b.issue(Issue{Line: line, Reason: reason, Type: truncateRunes(nativeType, 100), Retained: true, Severity: "semantic"})
	}
}

func (b *eventBuilder) emit(record, raw map[string]any, line int, role, kind, text string, extra map[string]any) {
	if record == nil {
		record = map[string]any{}
	}
	if raw == nil {
		raw = record
	}
	nativeID := firstValue(record["id"], record["message_id"], record["uuid"])
	sid := firstValue(record["session_id"], record["sessionId"], b.context["session_id"], any(b.options.FallbackSessionID))
	eventID, idOrigin := stringValue(nativeID), "native"
	if eventID == "" {
		eventID = "generated_" + hashShort([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%s", b.options.FallbackSessionID, line, len(b.events), kind)), 24)
		idOrigin = "generated"
	}
	sessionID := stringValue(sid)
	sidOrigin := "native"
	if sessionID == b.options.FallbackSessionID {
		sidOrigin = "generated"
	}
	sequence := len(b.events)
	nativeSequence := firstValue(record["sequence"], record["ordinal"])
	event := Event{"id": eventID, "id_origin": idOrigin, "session_id": sessionID, "session_id_origin": sidOrigin,
		"sequence": sequence, "line": line, "role": role, "kind": kind, "text": text, "inert": true,
		"timestamp": firstValue(record["timestamp"], raw["timestamp"]), "parent_session_id": firstValue(record["parent_session_id"], b.context["parent_session_id"]),
		"parent_event_id": firstValue(record["parentId"], record["parentUuid"]), "branch_id": record["branch_id"], "semantic_support": "specialized"}
	if nativeSequence != nil {
		event["native_sequence"] = nativeSequence
	}
	for key, value := range extra {
		if value != nil {
			event[key] = value
		}
	}
	if b.options.Adapter == AdapterCanonical {
		for _, key := range []string{"host_id", "host_name", "origin"} {
			if value, ok := b.current[key]; ok {
				event[key] = evidence(value)
			}
		}
	}
	if !b.preserved[line] {
		event["raw_record"] = evidence(raw)
		event["raw_preserved"] = true
		event["preservation_policy"] = "sanitized_json_with_embedded_media_descriptors"
		b.preserved[line] = true
	} else {
		event["raw_record_ref_line"] = line
	}
	cleaned, redactions, err := sanitizeEvent(event)
	if err != nil {
		b.issue(Issue{Line: line, Reason: "redaction_limit", Retained: false})
		return
	}
	if len(b.events) < 1_000_000 {
		b.events = append(b.events, cleaned)
	} else {
		b.issue(Issue{Line: line, Reason: "event_count_limit", Retained: false})
	}
	if len(redactions) > 0 {
		cleaned["redactions"] = redactions
	}
}

func content(value any) (string, []any, []map[string]any, []Issue) {
	texts := []string{}
	attachments := []any{}
	tools := []map[string]any{}
	issues := []Issue{}
	if value == nil {
		return "", attachments, tools, issues
	}
	if str, ok := value.(string); ok {
		return str, attachments, tools, issues
	}
	items, ok := value.([]any)
	if !ok {
		if part, ok := value.(map[string]any); ok {
			items = []any{part}
		} else {
			return printable(value), attachments, tools, issues
		}
	}
	for _, item := range items {
		if str, ok := item.(string); ok {
			texts = append(texts, str)
			continue
		}
		part, ok := item.(map[string]any)
		if !ok {
			attachments = append(attachments, map[string]any{"kind": "unsupported_content", "type": fmt.Sprintf("%T", item), "inert": true, "raw_content": item, "raw_preserved": true})
			continue
		}
		typ := stringValue(part["type"])
		switch typ {
		case "text", "input_text", "output_text":
			texts = append(texts, stringValue(part["text"]))
		case "thinking":
			texts = append(texts, stringValue(firstValue(part["text"], part["thinking"])))
		case "tool_use", "tool_result", "toolCall", "tool_call":
			tools = append(tools, part)
		case "image", "image_url", "input_image", "file", "input_file", "audio", "video":
			attachments = append(attachments, descriptor(part))
		default:
			attachments = append(attachments, map[string]any{"kind": "unsupported_content", "type": typ, "inert": true, "raw_content": part, "raw_preserved": true})
			issues = append(issues, Issue{Reason: "unsupported_content", Type: truncateRunes(typ, 100), Retained: true, Severity: "semantic"})
		}
	}
	return strings.Join(texts, "\n"), attachments, tools, issues
}

func descriptor(value any) map[string]any {
	input, ok := value.(map[string]any)
	if !ok {
		return map[string]any{"kind": "attachment", "description": truncateRunes(printable(value), 1000), "inert": true}
	}
	out := map[string]any{}
	for _, key := range []string{"type", "mime_type", "media_type", "name", "filename", "path", "url", "image_url"} {
		if v, ok := input[key]; ok {
			out[key] = v
		}
	}
	for _, key := range []string{"url", "image_url"} {
		if nested, ok := out[key].(map[string]any); ok {
			out[key] = nested["url"]
		}
		if str, ok := out[key].(string); ok && strings.HasPrefix(str, "data:") {
			out[key] = "[embedded media omitted]"
		}
	}
	if source, ok := input["source"].(map[string]any); ok {
		for _, key := range []string{"type", "media_type", "url"} {
			if v, ok := source[key]; ok {
				out[key] = v
			}
		}
	}
	out["kind"] = "attachment"
	out["inert"] = true
	return out
}

func evidence(value any) any {
	switch item := value.(type) {
	case string:
		if strings.HasPrefix(item, "data:") {
			sum := sha256.Sum256([]byte(item))
			return map[string]any{"inert": true, "kind": "embedded_media_descriptor", "sha256": hex.EncodeToString(sum[:]), "characters": utf8.RuneCountInString(item)}
		}
		return item
	case []any:
		out := make([]any, len(item))
		for i, v := range item {
			out[i] = evidence(v)
		}
		return out
	case map[string]any:
		if stringValue(item["type"]) == "credential_pin" {
			public := map[string]any{}
			for _, key := range []string{"type", "id", "parentId", "timestamp", "provider"} {
				if v, ok := item[key]; ok {
					public[key] = v
				}
			}
			secret := map[string]any{}
			for key, v := range item {
				if _, ok := public[key]; !ok {
					secret[key] = v
				}
			}
			public["credentials"] = secret
			return public
		}
		typ := stringValue(item["type"])
		if containsString([]string{"image", "image_url", "input_image", "file", "input_file", "audio", "video"}, typ) {
			result := descriptor(item)
			result["descriptor_only"] = true
			encoded, _ := json.Marshal(item)
			sum := sha256.Sum256(encoded)
			result["source_sha256"] = hex.EncodeToString(sum[:])
			return result
		}
		out := make(map[string]any, len(item))
		for key, v := range item {
			out[key] = evidence(v)
		}
		return out
	default:
		return value
	}
}

var (
	secretKeyPattern     = regexp.MustCompile(`(?i)(^|[_-])(?:private[_-]?key|secret[_-]?access[_-]?key|connection[_-]?string|api[_-]?key|access[_-]?token|refresh[_-]?token|auth[_-]?token|client[_-]?secret|password|passwd|pwd|secret|credentials?|authorization)$`)
	placeholderPattern   = regexp.MustCompile(`\[REDACTED:[a-z_]+(?::[0-9a-f]{64})?\]`)
	assignmentPattern    = regexp.MustCompile(`(?i)([A-Za-z][A-Za-z0-9_-]*(?:private[_ -]?key|secret[_ -]?access[_ -]?key|connection[_ -]?string|api[_ -]?key|access[_ -]?token|refresh[_ -]?token|auth[_ -]?token|client[_ -]?secret|password|passwd|pwd|secret|token|credentials?|authorization)["']?[ \t]*(?:=|:|\bis\b)[ \t]*)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\n\r,;}\]]+)`)
	pemPattern           = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]+-----.*?(?:-----END [A-Z0-9 ]+-----|$)`)
	credentialPattern    = regexp.MustCompile(`(^|[^A-Za-z0-9_-])(sk-(?:proj-|live-|test-)?[A-Za-z0-9_-]{8,}|(?:gh[pousr]_|github_pat_)[A-Za-z0-9_]{8,}|xox[baprs]-[A-Za-z0-9-]{8,}|(?:AKIA|ASIA)[A-Z0-9]{16}|AIza[A-Za-z0-9_-]{30,})([^A-Za-z0-9_-]|$)`)
	jwtPattern           = regexp.MustCompile(`(^|[^A-Za-z0-9_.-])(eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)([^A-Za-z0-9_.-]|$)`)
	authorizationPattern = regexp.MustCompile(`(?i)\b(?:Bearer|Basic)[ \t]+([A-Za-z0-9+/_.~=-]+)`)
	urlCredentialPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://([^\s/@]+(?::[^\s/@]*)?@)`)
	opaquePattern        = regexp.MustCompile(`[A-Za-z0-9+/_=-]{40,}`)
	hexTokenPattern      = regexp.MustCompile(`^[0-9a-fA-F]{40,}$`)
)

// redactionHints records which redaction patterns can possibly match a string.
// Every hint is a necessary condition of its pattern (a literal the pattern
// cannot match without), so skipping a pattern never changes the spans found.
type redactionHints struct {
	pem, credential, jwt, authorization, urlCredential, assignment, opaque bool
}

func newRedactionHints(text string) redactionHints {
	hints := redactionHints{
		pem:           strings.Contains(text, "-----BEGIN "),
		jwt:           strings.Contains(text, "eyJ"),
		urlCredential: strings.Contains(text, "://"),
		opaque:        hasOpaqueRun(text),
	}
	for _, literal := range credentialLiterals {
		if strings.Contains(text, literal) {
			hints.credential = true
			break
		}
	}
	// authorization and assignment are case-insensitive. regexp folds a few
	// non-ASCII runes onto ASCII letters (e.g. U+017F onto "s"), so the
	// lowercase probe is only exact for pure-ASCII text; otherwise run both.
	if !isASCII(text) {
		hints.authorization, hints.assignment = true, true
		return hints
	}
	lower := strings.ToLower(text)
	hints.authorization = strings.Contains(lower, "bearer") || strings.Contains(lower, "basic")
	for _, keyword := range assignmentKeywords {
		if strings.Contains(lower, keyword) {
			hints.assignment = true
			break
		}
	}
	return hints
}

// credentialLiterals are the fixed prefixes of credentialPattern's alternatives.
var credentialLiterals = []string{"sk-", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "xox", "AKIA", "ASIA", "AIza"}

// assignmentKeywords are the substrings every alternative of assignmentPattern's
// keyword group contains (private key, api key... all contain "key", and so on).
var assignmentKeywords = []string{"key", "string", "token", "secret", "password", "passwd", "pwd", "credential", "authorization"}

func isASCII(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// hasOpaqueRun reports whether text holds 40+ consecutive opaquePattern bytes.
func hasOpaqueRun(text string) bool {
	run := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '_' || c == '=' || c == '-' {
			if run++; run >= 40 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

type redactionSpan struct {
	start, end int
	kind       string
}

func redactString(text string, identity bool) (string, []map[string]any, error) {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxStringRunes {
		return "", nil, errors.New("unsupported redaction input")
	}
	for _, r := range text {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			return "", nil, errors.New("unsupported redaction input")
		}
	}
	spans := []redactionSpan{}
	add := func(pattern *regexp.Regexp, group int, kind string) {
		for _, loc := range pattern.FindAllStringSubmatchIndex(text, -1) {
			idx := 2 * group
			if idx+1 >= len(loc) || loc[idx] < 0 {
				continue
			}
			start, end := loc[idx], loc[idx+1]
			if end <= start {
				continue
			}
			if placeholderPattern.MatchString(text[start:end]) {
				continue
			}
			if kind == "opaque_token" {
				token := text[start:end]
				if hexTokenPattern.MatchString(token) || !strings.ContainsAny(token, "abcdefghijklmnopqrstuvwxyz") || !strings.ContainsAny(token, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") || !strings.ContainsAny(token, "0123456789") {
					continue
				}
			}
			spans = append(spans, redactionSpan{start, end, kind})
		}
	}
	// Each pattern is only run when a literal it cannot match without is
	// present; most transcript strings are prose and match none of them.
	hints := newRedactionHints(text)
	if hints.pem {
		add(pemPattern, 0, "pem")
	}
	if hints.credential {
		add(credentialPattern, 2, "credential")
	}
	if hints.jwt {
		add(jwtPattern, 2, "jwt")
	}
	if hints.authorization {
		add(authorizationPattern, 1, "authorization")
	}
	if hints.urlCredential {
		add(urlCredentialPattern, 1, "url_credentials")
	}
	if hints.assignment {
		add(assignmentPattern, 2, "assignment")
	}
	if hints.opaque {
		add(opaquePattern, 0, "opaque_token")
	}
	if len(spans) == 0 {
		return text, []map[string]any{}, nil
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	merged := []redactionSpan{}
	for _, span := range spans {
		if len(merged) > 0 && span.start < merged[len(merged)-1].end {
			if span.end > merged[len(merged)-1].end {
				merged[len(merged)-1].end = span.end
			}
			continue
		}
		merged = append(merged, span)
	}
	var out strings.Builder
	cursor := 0
	redactions := make([]map[string]any, 0, len(merged))
	for _, span := range merged {
		out.WriteString(text[cursor:span.start])
		token := text[span.start:span.end]
		suffix := ""
		if identity {
			sum := sha256.Sum256([]byte(token))
			suffix = ":" + hex.EncodeToString(sum[:])
		}
		out.WriteString("[REDACTED:" + span.kind + suffix + "]")
		redactions = append(redactions, map[string]any{"kind": span.kind, "start": span.start, "end": span.end})
		cursor = span.end
	}
	out.WriteString(text[cursor:])
	return out.String(), redactions, nil
}
func secretKey(key string) bool {
	value := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	// Every secret key name ends in one of assignmentKeywords; pure-ASCII keys
	// without any of them (the overwhelmingly common "type", "uuid"...) skip the
	// regexp and the suffix scan. Non-ASCII keys always take the full path.
	if isASCII(value) {
		found := false
		for _, keyword := range assignmentKeywords {
			if strings.Contains(value, keyword) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if secretKeyPattern.MatchString(value) {
		return true
	}
	for _, suffix := range []string{"_api_key", "_access_token", "_refresh_token", "_auth_token", "_client_secret", "_private_key", "_secret_access_key", "_connection_string", "_password", "_passwd", "_credentials", "_authorization"} {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}
func sanitizeValue(value any, depth int, budget *int) (any, error) {
	*budget = *budget - 1
	if depth > maxJSONDepth || *budget < 0 {
		return nil, errors.New("redaction limits exceeded")
	}
	switch item := value.(type) {
	case nil, bool, string, json.Number, int, int64, float64:
		if text, ok := item.(string); ok {
			if utf8.RuneCountInString(text) > maxStringRunes {
				return nil, errors.New("redaction limits exceeded")
			}
			*budget -= utf8.RuneCountInString(text)
			if *budget < 0 {
				return nil, errors.New("redaction limits exceeded")
			}
			cleaned, _, err := redactString(text, true)
			return cleaned, err
		}
		if number, ok := item.(json.Number); ok {
			if _, err := number.Float64(); err != nil {
				return nil, errors.New("nonfinite number")
			}
			return number, nil
		}
		if number, ok := item.(float64); ok && (math.IsNaN(number) || math.IsInf(number, 0)) {
			return nil, errors.New("nonfinite number")
		}
		return item, nil
	case []any:
		out := make([]any, len(item))
		for index, child := range item {
			cleaned, err := sanitizeValue(child, depth+1, budget)
			if err != nil {
				return nil, err
			}
			out[index] = cleaned
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(item))
		for key, child := range item {
			cleanKey, _, err := redactString(key, true)
			if err != nil {
				return nil, err
			}
			if _, exists := out[cleanKey]; exists {
				return nil, errors.New("redacted metadata key collision")
			}
			if secretKey(key) && child != nil && child != false && child != "" {
				if text, ok := child.(string); ok && placeholderPattern.MatchString(text) {
					out[cleanKey] = text
					continue
				}
				if _, err := sanitizeValue(child, depth+1, budget); err != nil {
					return nil, err
				}
				encoded, err := json.Marshal(child)
				if err != nil {
					return nil, errors.New("invalid sensitive metadata")
				}
				sum := sha256.Sum256(encoded)
				out[cleanKey] = "[REDACTED:metadata:" + hex.EncodeToString(sum[:]) + "]"
				continue
			}
			cleaned, err := sanitizeValue(child, depth+1, budget)
			if err != nil {
				return nil, err
			}
			out[cleanKey] = cleaned
		}
		return out, nil
	default:
		return nil, errors.New("unsupported redaction input")
	}
}
func sanitizeEvent(event Event) (Event, []map[string]any, error) {
	text := stringValue(event["text"])
	cleanText, redactions, err := redactString(text, false)
	if err != nil {
		return nil, nil, err
	}
	rest := map[string]any{}
	for key, value := range event {
		if key != "text" {
			rest[key] = value
		}
	}
	budget := 2 * maxStringRunes
	cleaned, err := sanitizeValue(rest, 0, &budget)
	if err != nil {
		return nil, nil, err
	}
	fields, ok := cleaned.(map[string]any)
	if !ok {
		return nil, nil, errors.New("invalid event")
	}
	fields["text"] = cleanText
	fields["redactions"] = redactions
	fields["redaction_version"] = RedactionVersion
	fields["safe"] = true
	return Event(fields), redactions, nil
}

func printable(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}
func firstValue(values ...any) any {
	for _, value := range values {
		if value != nil && value != "" {
			return value
		}
	}
	return nil
}
func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch item := value.(type) {
	case string:
		return item
	case json.Number:
		return item.String()
	case float64:
		return strconv.FormatFloat(item, 'f', -1, 64)
	case int:
		return strconv.Itoa(item)
	case int64:
		return strconv.FormatInt(item, 10)
	default:
		return ""
	}
}
func firstString(values ...any) string {
	for _, value := range values {
		if s := stringValue(value); s != "" {
			return s
		}
	}
	return ""
}
func isRole(role string) bool {
	return containsString([]string{"user", "assistant", "system", "developer", "tool", "service", "toolResult"}, role)
}
func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
func cloneMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
func hashShort(value []byte, length int) string {
	sum := sha256.Sum256(value)
	hexValue := hex.EncodeToString(sum[:])
	if length > len(hexValue) {
		length = len(hexValue)
	}
	return hexValue[:length]
}
func truncateRunes(value string, limit int) string {
	// utf8.RuneCountInString counts exactly the runes []rune(value) would
	// produce, so short strings are returned without the rune-slice copy.
	if len(value) <= limit || utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

// SessionActivity returns the latest original event timestamp, falling back to
// source mtime supplied by the caller; capture time is never conversation activity.
func SessionActivity(events []Event, fallback time.Time) float64 {
	latest := float64(0)
	for _, event := range events {
		value := timestampValue(event["timestamp"])
		if value == 0 {
			if raw, ok := event["raw_record"].(map[string]any); ok {
				value = timestampValue(raw["timestamp"])
			}
		}
		if value > latest {
			latest = value
		}
	}
	if latest == 0 && !fallback.IsZero() {
		return float64(fallback.UnixNano()) / 1e9
	}
	return latest
}
func timestampValue(value any) float64 {
	switch item := value.(type) {
	case json.Number:
		n, err := item.Float64()
		if err == nil {
			return epochSeconds(n)
		}
	case float64:
		return epochSeconds(item)
	case int:
		return epochSeconds(float64(item))
	case int64:
		return epochSeconds(float64(item))
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, strings.ReplaceAll(item, "Z", "+00:00"))
		if err == nil {
			return float64(parsed.UnixNano()) / 1e9
		}
		parsed, err = time.Parse("2006-01-02T15:04:05.999999999-07:00", item)
		if err == nil {
			return float64(parsed.UnixNano()) / 1e9
		}
	}
	return 0
}
func epochSeconds(value float64) float64 {
	if value > 100_000_000_000 {
		value /= 1000
	}
	if !math.IsInf(value, 0) && !math.IsNaN(value) && value > 0 {
		return value
	}
	return 0
}

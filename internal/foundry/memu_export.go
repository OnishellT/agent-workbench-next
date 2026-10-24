// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"workbench/internal/hosts"
)

type memuExportCitation struct {
	EventIndex int    `json:"event_index"`
	EventID    string `json:"event_id,omitempty"`
	SourceLine int    `json:"source_line"`
	Role       string `json:"role"`
	Kind       string `json:"kind"`
	Exported   bool   `json:"exported"`
}

type memuExportSession struct {
	ID        string               `json:"id"`
	SourceID  string               `json:"source_id"`
	Revision  string               `json:"revision"`
	File      string               `json:"file"`
	Host      string               `json:"host"`
	Identity  *hosts.Identity      `json:"identity,omitempty"`
	Project   string               `json:"project"`
	Citations []memuExportCitation `json:"citations"`
}

type memuExportManifest struct {
	GeneratedAt float64             `json:"generated_at"`
	Sessions    []memuExportSession `json:"sessions"`
}

type memuSessionRow struct {
	ID       string
	SourceID string
	Revision string
	Host     string
}

type memuEventRow struct {
	Index     int
	EventID   string
	Line      int
	Role      string
	Kind      string
	Text      string
	Timestamp string
	ToolName  string
	CallID    string
	Evidence  string
}

func (m *Module) exportMemuEvidence(ctx context.Context, exportDir string) (memuExportManifest, *APIError) {
	if err := resetMemuDir(exportDir); err != nil {
		return memuExportManifest{}, internalError(err)
	}
	sessions, err := queryAll(ctx, m.db, func(rows *sql.Rows) (memuSessionRow, error) {
		var row memuSessionRow
		return row, rows.Scan(&row.ID, &row.SourceID, &row.Revision, &row.Host)
	}, `SELECT s.id,s.source_id,s.revision,s.host_id
		FROM sessions s JOIN sources src ON src.id=s.source_id
		WHERE src.enabled=1 ORDER BY s.id`)
	if err != nil {
		return memuExportManifest{}, internalError(err)
	}

	manifest := memuExportManifest{GeneratedAt: nowSeconds(), Sessions: make([]memuExportSession, 0, len(sessions))}
	for _, session := range sessions {
		events, current, err := m.memuSessionEvents(ctx, session)
		if err != nil {
			return memuExportManifest{}, internalError(err)
		}
		if !current {
			continue // source disabled or session changed since the listing
		}
		sum := sha256.Sum256([]byte(session.ID))
		name := hex.EncodeToString(sum[:12]) + ".jsonl"
		var data bytes.Buffer
		encoder := json.NewEncoder(&data)
		encoder.SetEscapeHTML(false)
		citations := make([]memuExportCitation, 0, len(events))
		project := ""
		for _, event := range events {
			if project == "" && event.Kind == "session_metadata" {
				project = memuProjectFromEvidence(event.Evidence)
			}
			record, exported := memuCodexRecord(event)
			citations = append(citations, memuExportCitation{
				EventIndex: event.Index,
				EventID:    event.EventID,
				SourceLine: event.Line,
				Role:       event.Role,
				Kind:       event.Kind,
				Exported:   exported,
			})
			if exported {
				if err := encoder.Encode(record); err != nil {
					return memuExportManifest{}, internalError(err)
				}
			}
		}
		// Keep processor sessions out of their own learning input.
		if project == filepath.Join(m.stateDir, "memu") || strings.HasPrefix(project, filepath.Join(m.stateDir, "memu")+string(filepath.Separator)) || filepath.Dir(project) == "/tmp" && strings.HasPrefix(filepath.Base(project), "workbench-memu-") {
			continue
		}
		host := session.Host
		if host == "" {
			host = "local"
		}
		var identity *hosts.Identity
		if host != "local" {
			if m.hostScope == nil {
				return memuExportManifest{}, &APIError{Status: 409, Code: "host_scope_denied", Message: "Host no disponible."}
			}
			confirmed, identityErr := m.hostScope.Identity(ctx, host)
			if identityErr != nil {
				return memuExportManifest{}, &APIError{Status: 409, Code: "host_scope_denied", Message: "Identidad del host no confirmada."}
			}
			identity = &confirmed
		}
		if err := writePrivateFile(filepath.Join(exportDir, name), data.Bytes()); err != nil {
			return memuExportManifest{}, internalError(err)
		}
		manifest.Sessions = append(manifest.Sessions, memuExportSession{Identity: identity,
			ID:        session.ID,
			SourceID:  session.SourceID,
			Revision:  session.Revision,
			File:      name,
			Host:      host,
			Project:   project,
			Citations: citations,
		})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return memuExportManifest{}, internalError(err)
	}
	if err := writePrivateFile(filepath.Join(exportDir, "manifest.json"), append(data, '\n')); err != nil {
		return memuExportManifest{}, internalError(err)
	}
	return manifest, nil
}

// memuSessionEvents reads a session's events in one read transaction together with a
// recheck that its source is still enabled and that session, revision and host are the
// ones listed for export. ok=false means the authorization changed meanwhile: the
// session must not be exported.
func (m *Module) memuSessionEvents(ctx context.Context, session memuSessionRow) (events []memuEventRow, ok bool, err error) {
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions s JOIN sources src ON src.id=s.source_id
		WHERE s.id=? AND s.source_id=? AND s.revision=? AND s.host_id=? AND src.enabled=1`, session.ID, session.SourceID, session.Revision, session.Host).Scan(&current); err != nil {
		return nil, false, err
	}
	if current == 0 {
		return nil, false, nil
	}
	events, err = queryAll(ctx, tx, func(rows *sql.Rows) (memuEventRow, error) {
		var event memuEventRow
		return event, rows.Scan(&event.Index, &event.EventID, &event.Line, &event.Role, &event.Kind, &event.Text, &event.Timestamp, &event.ToolName, &event.CallID, &event.Evidence)
	}, `SELECT event_index,event_id,source_line,role,kind,text,timestamp_json,tool_name,call_id,evidence_json
		FROM events WHERE session_id=? AND revision=? ORDER BY event_index`, session.ID, session.Revision)
	if err != nil {
		return nil, false, err
	}
	return events, true, nil
}

func memuProjectFromEvidence(raw string) string {
	var evidence map[string]any
	if json.Unmarshal([]byte(raw), &evidence) != nil {
		return ""
	}
	metadata, _ := evidence["metadata"].(map[string]any)
	for _, key := range []string{"cwd", "project"} {
		if value, ok := metadata[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func memuCodexRecord(event memuEventRow) (map[string]any, bool) {
	record := map[string]any{}
	var timestamp string
	if json.Unmarshal([]byte(event.Timestamp), &timestamp) == nil && timestamp != "" {
		record["timestamp"] = timestamp
	}
	switch {
	case (event.Role == "user" || event.Role == "assistant") && (event.Kind == "message" || event.Kind == "notification"):
		contentType := "output_text"
		if event.Role == "user" {
			contentType = "input_text"
		}
		record["type"] = "response_item"
		record["payload"] = map[string]any{
			"type": "message",
			"role": event.Role,
			"content": []any{
				map[string]any{"type": contentType, "text": event.Text},
			},
		}
		return record, true
	case event.Kind == "tool_call":
		record["type"] = "response_item"
		record["payload"] = map[string]any{
			"type":      "function_call",
			"name":      event.ToolName,
			"arguments": event.Text,
			"call_id":   event.CallID,
		}
		return record, true
	case event.Kind == "tool_result":
		record["type"] = "response_item"
		record["payload"] = map[string]any{
			"type":    "function_call_output",
			"output":  event.Text,
			"call_id": event.CallID,
		}
		return record, true
	default:
		return nil, false
	}
}

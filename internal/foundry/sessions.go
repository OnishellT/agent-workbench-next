// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

func (m *Module) listConversations(w http.ResponseWriter, r *http.Request) {
	limit, offset, apiErr := queryLimit(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	sourceID := r.URL.Query().Get("source_id")
	adapter := r.URL.Query().Get("adapter")
	status := r.URL.Query().Get("status")
	hostFilter := r.URL.Query().Get("host_id")
	where := []string{"1=1"}
	args := make([]any, 0, 8)
	if sourceID != "" {
		where = append(where, "s.source_id=?")
		args = append(args, sourceID)
	}
	if adapter != "" {
		where = append(where, "src.adapter=?")
		args = append(args, adapter)
	}
	if query != "" {
		where = append(where, `(s.id LIKE ? ESCAPE '\' OR s.title LIKE ? ESCAPE '\' OR s.path LIKE ? ESCAPE '\')`)
		pattern := "%" + escapeLike(query) + "%"
		args = append(args, pattern, pattern, pattern)
	}
	if status != "" {
		switch status {
		case "complete", "partial", "unknown", "error":
			if status == "error" {
				status = "partial"
			}
			where = append(where, "s.coverage_json LIKE ?")
			args = append(args, "%\"status\":\""+status+"\"%")
		case "suggested":
			where = append(where, "s.completion=?")
			args = append(args, status)
		default:
			writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_status", Message: "El estado de conversación no es válido."})
			return
		}
	}
	if hostFilter != "" {
		where = append(where, "s.host_id=?")
		args = append(args, hostFilter)
	}
	rows, err := m.db.QueryContext(r.Context(), `SELECT s.id,s.source_id,s.path,s.title,s.revision,s.updated,s.captured_at,s.activity_at,s.completion,s.coverage_json,s.host_id,src.adapter,(SELECT COUNT(*) FROM tasks t WHERE t.session_id=s.id AND t.revision=s.revision) FROM sessions s JOIN sources src ON src.id=s.source_id WHERE `+strings.Join(where, " AND ")+` ORDER BY s.updated DESC,s.id LIMIT 10000`, args...)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	defer rows.Close()
	visibility := m.newHostVisibility(r.Context())
	all := make([]ConversationDTO, 0)
	for rows.Next() {
		item, err := scanConversation(rows)
		if err != nil {
			writeAPIError(w, internalError(err))
			return
		}
		if item.HostID != "" {
			name, allowed := visibility.name(item.HostID)
			if !allowed {
				continue
			}
			item.HostName = name
		}
		all = append(all, item)
	}
	if err := rows.Err(); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	total := len(all)
	end := offset + limit
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}
	page := all[offset:end]
	_ = rows.Close()
	for i := range page {
		page[i].Project = m.conversationProject(r.Context(), page[i].ID, page[i].Revision)
	}
	next := ""
	if end < total {
		next = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": page, "total_sessions": total, "next_cursor": next})
}

// escapeLike keeps the operator's search term literal: a % or _ written in a title or a
// partial name is a character of it, never the wildcard that answered unrelated rows.
func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func (m *Module) conversationDetail(w http.ResponseWriter, r *http.Request, rawID string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	conversation, err := m.loadConversation(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "conversation_not_found", Message: "No se encontró la conversación."})
		return
	}
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if conversation.HostID != "" {
		name, allowed := m.hostName(r.Context(), conversation.HostID)
		if !allowed {
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "conversation_not_found", Message: "No se encontró la conversación en el ámbito de hosts actual."})
			return
		}
		conversation.HostName = name
	}
	revisions, err := m.revisionList(r.Context(), id)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": conversation, "revisions": revisions})
}

func (m *Module) conversationRevisions(w http.ResponseWriter, r *http.Request, rawID string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	conversation, err := m.loadConversation(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "conversation_not_found", Message: "No se encontró la conversación."})
		return
	}
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if conversation.HostID != "" {
		if _, allowed := m.hostName(r.Context(), conversation.HostID); !allowed {
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "conversation_not_found", Message: "No se encontró la conversación en el ámbito de hosts actual."})
			return
		}
	}
	revisions, err := m.revisionList(r.Context(), id)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "latest_revision": conversation.Revision, "revisions": revisions})
}

func (m *Module) conversationRevision(w http.ResponseWriter, r *http.Request, rawID, rawRevision string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	revision, apiErr := pathValue(rawRevision)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	conversation, err := m.loadConversation(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "conversation_not_found", Message: "No se encontró la conversación."})
		return
	}
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if conversation.HostID != "" {
		name, allowed := m.hostName(r.Context(), conversation.HostID)
		if !allowed {
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "conversation_not_found", Message: "No se encontró la conversación en el ámbito de hosts actual."})
			return
		}
		conversation.HostName = name
	}
	if revision == "latest" {
		revision = conversation.Revision
	}
	var created float64
	var coverageJSON string
	err = m.db.QueryRowContext(r.Context(), `SELECT created,coverage_json FROM revisions WHERE session_id=? AND revision=?`, id, revision).Scan(&created, &coverageJSON)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "revision_not_found", Message: "No existe esa revisión inmutable."})
		return
	}
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	var coverage CoverageSummary
	if err := json.Unmarshal([]byte(coverageJSON), &coverage); err != nil {
		coverage = CoverageSummary{Status: "unknown"}
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_limit", Message: "El límite debe ser un entero positivo."})
			return
		}
		if value > maxPageLimit {
			value = maxPageLimit
		}
		limit = value
	}
	cursor := 0
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_cursor", Message: "El cursor debe ser un índice no negativo."})
			return
		}
		cursor = value
	}
	compact := r.URL.Query().Get("compact") == "1"
	rows, err := m.db.QueryContext(r.Context(), `SELECT event_index,event_id,role,kind,text,timestamp_json,channel,tool_name,call_id,attachments_json,inert,completion_suggested,evidence_json FROM events WHERE session_id=? AND revision=? AND event_index>=? ORDER BY event_index LIMIT ?`, id, revision, cursor, limit+1)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	defer rows.Close()
	events := make([]any, 0, limit)
	lastIndex := 0
	for rows.Next() {
		var event ConversationEvent
		var timestampJSON, attachmentsJSON, evidenceJSON string
		var inert, completion int
		if err := rows.Scan(&event.EventIndex, &event.ID, &event.Role, &event.Kind, &event.Text, &timestampJSON, &event.Channel, &event.ToolName, &event.CallID, &attachmentsJSON, &inert, &completion, &evidenceJSON); err != nil {
			writeAPIError(w, internalError(err))
			return
		}
		event.Inert = inert != 0
		if timestampJSON != "" {
			_ = json.Unmarshal([]byte(timestampJSON), &event.Timestamp)
		}
		if err := json.Unmarshal([]byte(attachmentsJSON), &event.Attachments); err != nil {
			event.Attachments = []any{}
		}
		textLimit := 64_000
		if compact {
			textLimit = 4000
		}
		if utf8.RuneCountInString(event.Text) > textLimit {
			event.Text = truncateRunes(event.Text, textLimit)
			event.EventTextTruncated = true
		}
		if len(events) < limit { // the look-ahead row belongs to the next page
			lastIndex = event.EventIndex
		}
		if compact {
			events = append(events, event)
			continue
		}
		// Expose sanitized captured evidence; canonical fields keep precedence.
		preserved := map[string]any{}
		if evidenceJSON != "" {
			if err := json.Unmarshal([]byte(evidenceJSON), &preserved); err != nil {
				preserved = map[string]any{}
			}
		}
		preserved["event_index"] = event.EventIndex
		preserved["id"], preserved["role"], preserved["kind"], preserved["text"] = event.ID, event.Role, event.Kind, event.Text
		preserved["inert"] = event.Inert
		if timestampJSON != "" {
			preserved["timestamp"] = event.Timestamp
		}
		if event.Channel != "" {
			preserved["channel"] = event.Channel
		}
		if event.ToolName != "" {
			preserved["tool_name"] = event.ToolName
		}
		if event.CallID != "" {
			preserved["call_id"] = event.CallID
		}
		preserved["attachments"] = event.Attachments
		if event.EventTextTruncated {
			preserved["event_text_truncated"] = true
		}
		events = append(events, preserved)
	}
	if err := rows.Err(); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	next := ""
	if hasMore && len(events) > 0 {
		next = strconv.Itoa(lastIndex + 1)
	}
	var eventCount int
	if err := m.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM events WHERE session_id=? AND revision=?`, id, revision).Scan(&eventCount); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	var historicalTaskCount int
	if err := m.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM tasks WHERE session_id=? AND revision=?`, id, revision).Scan(&historicalTaskCount); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	tasks, err := m.tasksForRevision(r.Context(), id, revision)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	latestRevision := conversation.Revision
	conversation.Revision = revision
	conversation.Coverage = coverage
	conversation.Completion = revisionCompletion(r.Context(), m.db, id, revision)
	conversationData := map[string]any{"id": conversation.ID, "source_id": conversation.SourceID, "title": conversation.Title, "revision": revision, "latest_revision": latestRevision, "is_latest": revision == latestRevision, "revision_created": created, "coverage": coverage, "event_count": eventCount, "completion": conversation.Completion}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": conversationData, "events": events, "next_cursor": next, "historical_task_count": historicalTaskCount, "tasks": tasks})
}

func (m *Module) loadConversation(ctx context.Context, id string) (ConversationDTO, error) {
	row := m.db.QueryRowContext(ctx, `SELECT s.id,s.source_id,s.path,s.title,s.revision,s.updated,s.captured_at,s.activity_at,s.completion,s.coverage_json,s.host_id,src.adapter,(SELECT COUNT(*) FROM tasks t WHERE t.session_id=s.id AND t.revision=s.revision) FROM sessions s JOIN sources src ON src.id=s.source_id WHERE s.id=?`, id)
	item, err := scanConversation(row)
	if err == nil {
		item.Project = m.conversationProject(ctx, item.ID, item.Revision)
	}
	return item, err
}

func scanConversation(row interface{ Scan(...any) error }) (ConversationDTO, error) {
	var item ConversationDTO
	var coverageJSON, adapter string
	err := row.Scan(&item.ID, &item.SourceID, &item.Path, &item.Title, &item.Revision, &item.Updated, &item.CapturedAt, &item.ActivityAt, &item.Completion, &coverageJSON, &item.HostID, &adapter, &item.TaskCount)
	if err != nil {
		return item, err
	}
	item.Adapter = Adapter(adapter)
	if err := json.Unmarshal([]byte(coverageJSON), &item.Coverage); err != nil {
		item.Coverage = CoverageSummary{Status: "unknown"}
	}
	return item, nil
}

func (m *Module) revisionList(ctx context.Context, sessionID string) ([]RevisionDTO, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT revision,created,coverage_json FROM revisions WHERE session_id=? ORDER BY created DESC,revision DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	revisions := make([]RevisionDTO, 0)
	for rows.Next() {
		var item RevisionDTO
		var coverageJSON string
		if err := rows.Scan(&item.Revision, &item.Created, &coverageJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(coverageJSON), &item.Coverage); err != nil {
			item.Coverage = CoverageSummary{Status: "unknown"}
		}
		revisions = append(revisions, item)
	}
	return revisions, rows.Err()
}

func (m *Module) hostName(ctx context.Context, id string) (string, bool) {
	if id == "" || id == "local" {
		return "", true
	}
	if m.hostScope == nil {
		return "", false
	}
	host, err := m.hostScope.Get(ctx, id)
	if err != nil || host.ID != id || host.ID == "local" {
		return "", false
	}
	return host.Name, true
}

// hostVisibility memoises hostName for one request: list endpoints test every row
// and most rows share a host, so the host registry is asked once per host.
type hostVisibility struct {
	m      *Module
	ctx    context.Context
	lookup map[string]hostLookup
}

type hostLookup struct {
	name    string
	allowed bool
}

func (m *Module) newHostVisibility(ctx context.Context) *hostVisibility {
	return &hostVisibility{m: m, ctx: ctx, lookup: map[string]hostLookup{}}
}

func (h *hostVisibility) name(id string) (string, bool) {
	if found, ok := h.lookup[id]; ok {
		return found.name, found.allowed
	}
	name, allowed := h.m.hostName(h.ctx, id)
	h.lookup[id] = hostLookup{name, allowed}
	return name, allowed
}

func (m *Module) tasksForRevision(ctx context.Context, sessionID, revision string) ([]TaskDTO, error) {
	ids, err := queryAll(ctx, m.db, func(rows *sql.Rows) (string, error) {
		var id string
		return id, rows.Scan(&id)
	}, `SELECT id FROM tasks WHERE session_id=? AND revision=? ORDER BY first_event,last_event,created,id`, sessionID, revision)
	if err != nil {
		return nil, err
	}
	tasks := make([]TaskDTO, 0, len(ids))
	for _, id := range ids {
		task, err := m.loadTaskDTO(ctx, id)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func revisionCompletion(ctx context.Context, db *sql.DB, sessionID, revision string) string {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE session_id=? AND revision=? AND completion_suggested=1`, sessionID, revision).Scan(&count); err != nil {
		return "unknown"
	}
	if count > 0 {
		return "suggested"
	}
	return "unknown"
}

func (m *Module) conversationProject(ctx context.Context, id, revision string) string {
	var raw string
	if err := m.db.QueryRowContext(ctx, "SELECT evidence_json FROM events WHERE session_id=? AND revision=? AND kind='session_metadata' ORDER BY event_index LIMIT 1", id, revision).Scan(&raw); err != nil {
		return ""
	}
	return memuProjectFromEvidence(raw)
}

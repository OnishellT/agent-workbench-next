// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type taskCreateRequest struct {
	SessionID  string         `json:"session_id"`
	Revision   string         `json:"revision"`
	FirstEvent int            `json:"first_event"`
	LastEvent  int            `json:"last_event"`
	Title      string         `json:"title,omitempty"`
	Citations  []TaskCitation `json:"citations,omitempty"`
}

type completionRequest struct {
	Completed          *bool  `json:"completed"`
	ExpectedGeneration *int64 `json:"expected_generation,omitempty"`
}

type labelRequest struct {
	Label              string  `json:"label"`
	Feedback           *string `json:"feedback,omitempty"`
	ExpectedGeneration *int64  `json:"expected_generation,omitempty"`
}

type feedbackRequest struct {
	Feedback           *string `json:"feedback"`
	ExpectedGeneration *int64  `json:"expected_generation,omitempty"`
}

type taskRecord struct {
	ID                    string
	SessionID             string
	Revision              string
	FirstEvent            int
	LastEvent             int
	Title                 string
	Label                 string
	Feedback              string
	Completed             bool
	CompletionGeneration  int64
	CompletionConfirmedAt sql.NullFloat64
	Created               float64
	Updated               float64
	SourceID              string
	HostID                string
	CoverageJSON          string
}

func (m *Module) listTasks(w http.ResponseWriter, r *http.Request) {
	limit, offset, apiErr := queryLimit(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "incomplete" && status != "pending" && status != "correct" && status != "incorrect" {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_status", Message: "El estado de tarea no es válido."})
		return
	}
	where := []string{"1=1"}
	args := make([]any, 0, 8)
	if sessionID := r.URL.Query().Get("session_id"); sessionID != "" {
		where = append(where, "t.session_id=?")
		args = append(args, sessionID)
	}
	if sourceID := r.URL.Query().Get("source_id"); sourceID != "" {
		where = append(where, "s.source_id=?")
		args = append(args, sourceID)
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query != "" {
		where = append(where, `(t.title LIKE ? ESCAPE '\' OR t.feedback LIKE ? ESCAPE '\')`)
		pattern := "%" + escapeLike(query) + "%"
		args = append(args, pattern, pattern)
	}
	if status == "incomplete" {
		where = append(where, "t.completed=0")
	}
	// The screen shows a pending task as one that is already completed and not valued yet,
	// so the filter answers that state and not the stored label on its own.
	if status == "pending" || status == "correct" || status == "incorrect" {
		where = append(where, "t.completed=1 AND t.label=?")
		args = append(args, status)
	}
	rows, err := m.db.QueryContext(r.Context(), `SELECT t.id,s.host_id FROM tasks t JOIN sessions s ON s.id=t.session_id WHERE `+strings.Join(where, " AND ")+` ORDER BY t.updated DESC,t.created DESC,t.id LIMIT 100000`, args...)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	// Host visibility is decided on the cheap (id, host_id) rows so that only the
	// requested page is loaded in full (two queries per task).
	visibility := m.newHostVisibility(r.Context())
	visible := make([]string, 0)
	for rows.Next() {
		var id, hostID string
		if err := rows.Scan(&id, &hostID); err != nil {
			rows.Close()
			writeAPIError(w, internalError(err))
			return
		}
		if hostID != "" {
			if _, ok := visibility.name(hostID); !ok {
				continue
			}
		}
		visible = append(visible, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeAPIError(w, internalError(err))
		return
	}
	rows.Close()
	total := len(visible)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := make([]TaskDTO, 0, end-offset)
	for _, id := range visible[offset:end] {
		task, err := m.loadTaskDTO(r.Context(), id)
		if err != nil {
			writeAPIError(w, internalError(err))
			return
		}
		page = append(page, task)
	}
	next := ""
	if end < total {
		next = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": page, "total_tasks": total, "next_cursor": next})
}

func (m *Module) createTask(w http.ResponseWriter, r *http.Request) {
	var request taskCreateRequest
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if strings.TrimSpace(request.SessionID) == "" || strings.TrimSpace(request.Revision) == "" || request.FirstEvent < 0 || request.LastEvent < request.FirstEvent {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_range", Message: "La revisión y el rango inclusivo de eventos son obligatorios."})
		return
	}
	var latest string
	var coverageJSON string
	var sourceID, hostID string
	if err := m.db.QueryRowContext(r.Context(), `SELECT s.revision,s.coverage_json,s.source_id,s.host_id FROM sessions s WHERE s.id=?`, request.SessionID).Scan(&latest, &coverageJSON, &sourceID, &hostID); errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "conversation_not_found", Message: "No se encontró la conversación."})
		return
	} else if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if latest != request.Revision {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "stale_revision", Message: "La conversación cambió; vuelve a seleccionar el rango en la revisión actual."})
		return
	}
	if hostID != "" {
		if _, ok := m.hostName(r.Context(), hostID); !ok {
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "conversation_not_found", Message: "No se encontró la conversación en el ámbito de hosts actual."})
			return
		}
	}
	var eventCount int
	if err := m.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM events WHERE session_id=? AND revision=?`, request.SessionID, request.Revision).Scan(&eventCount); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if request.LastEvent >= eventCount {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_range", Message: "El rango excede los eventos capturados."})
		return
	}
	var firstRole, firstText string
	if err := m.db.QueryRowContext(r.Context(), `SELECT role,text FROM events WHERE session_id=? AND revision=? AND event_index=?`, request.SessionID, request.Revision, request.FirstEvent).Scan(&firstRole, &firstText); err != nil {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_range", Message: "No se encontró el evento inicial de la tarea."})
		return
	}
	if firstRole != "user" || strings.TrimSpace(firstText) == "" {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "task_must_start_with_user", Message: "Una tarea debe empezar con una petición humana no vacía."})
		return
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = titleFromEvents([]Event{{"role": "user", "text": firstText}}, "")
	}
	title, apiErr := cleanUserText(title, maxTaskTitle)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	citations, apiErr := m.validateTaskCitations(r.Context(), request, firstText)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	// The duplicate and overlap decisions are read and the task is created inside the same
	// transaction: two tabs sending the same new range at once cannot both win, they see the
	// row the first one committed and answer the same task.
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	defer tx.Rollback()
	var taskID string
	if err := tx.QueryRowContext(r.Context(), `SELECT id FROM tasks WHERE session_id=? AND revision=? AND first_event=? AND last_event=?`, request.SessionID, request.Revision, request.FirstEvent, request.LastEvent).Scan(&taskID); err == nil {
		// The transaction owns the only connection, so it is closed before reading the task.
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			writeAPIError(w, internalError(err))
			return
		}
		task, err := m.loadTaskDTO(r.Context(), taskID)
		if err != nil {
			writeAPIError(w, internalError(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"task": task, "reused": true, "snapshot_is_latest": latest == request.Revision})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, internalError(err))
		return
	}
	var overlaps int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM tasks WHERE session_id=? AND revision=? AND first_event<=? AND last_event>=?`, request.SessionID, request.Revision, request.LastEvent, request.FirstEvent).Scan(&overlaps); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if overlaps > 0 {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "task_range_overlap", Message: "El rango se solapa con otra tarea de esta revisión."})
		return
	}
	taskID, err = newID("task_")
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	now := nowSeconds()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO tasks(id,session_id,revision,first_event,last_event,title,label,feedback,completed,completion_generation,created,updated) VALUES(?,?,?,?,?,?,'pending','',0,0,?,?)`, taskID, request.SessionID, request.Revision, request.FirstEvent, request.LastEvent, title, now, now)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	for index, citation := range citations {
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO task_citations(task_id,citation_index,event_index,quote) VALUES(?,?,?,?)`, taskID, index, citation.EventIndex, citation.Quote); err != nil {
			writeAPIError(w, internalError(err))
			return
		}
	}
	if err := appendAudit(r.Context(), tx, "task", taskID, "task_created", map[string]any{"session_id": request.SessionID, "revision": request.Revision, "first_event": request.FirstEvent, "last_event": request.LastEvent, "source": "manual"}); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	task, err := m.loadTaskDTO(r.Context(), taskID)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"task": task, "reused": false, "snapshot_is_latest": latest == request.Revision})
	_ = coverageJSON
	_ = sourceID
}

func (m *Module) validateTaskCitations(ctx context.Context, request taskCreateRequest, firstText string) ([]TaskCitation, *APIError) {
	citations := append([]TaskCitation(nil), request.Citations...)
	if len(citations) == 0 {
		quote := truncateRunes(firstText, maxCitationQuote)
		citations = []TaskCitation{{EventIndex: request.FirstEvent, Quote: quote}}
	}
	if len(citations) > 16 {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "invalid_citation", Message: "Se permiten hasta 16 citas por tarea."}
	}
	hasFirst := false
	for _, citation := range citations {
		if citation.EventIndex < request.FirstEvent || citation.EventIndex > request.LastEvent || strings.TrimSpace(citation.Quote) == "" || utf8.RuneCountInString(citation.Quote) > maxCitationQuote {
			return nil, &APIError{Status: http.StatusBadRequest, Code: "invalid_citation", Message: "La cita debe pertenecer al rango y tener hasta 2000 caracteres."}
		}
		var text string
		if err := m.db.QueryRowContext(ctx, `SELECT text FROM events WHERE session_id=? AND revision=? AND event_index=?`, request.SessionID, request.Revision, citation.EventIndex).Scan(&text); err != nil || !strings.Contains(text, citation.Quote) {
			return nil, &APIError{Status: http.StatusBadRequest, Code: "citation_not_found", Message: "La cita exacta no aparece en el evento original de su rango."}
		}
		if citation.EventIndex == request.FirstEvent {
			hasFirst = true
		}
	}
	if !hasFirst {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "initial_citation_required", Message: "Falta una cita exacta de la petición inicial."}
	}
	sort.Slice(citations, func(i, j int) bool {
		if citations[i].EventIndex != citations[j].EventIndex {
			return citations[i].EventIndex < citations[j].EventIndex
		}
		return citations[i].Quote < citations[j].Quote
	})
	return citations, nil
}

// Cada respuesta de una tarea existente lleva su selección, para que guardar no la borre.
func (m *Module) publishTaskCases(ctx context.Context, task *TaskDTO) error {
	generation, cases, err := m.selectedLearningCases(ctx, task.ID)
	if err != nil {
		return err
	}
	task.CasesGeneration = &generation
	if len(cases) == 0 {
		return nil
	}
	task.Cases = make([]TaskCaseRef, 0, len(cases))
	for _, row := range cases {
		task.Cases = append(task.Cases, TaskCaseRef{ID: row.ID, Title: row.Title, Domain: row.Domain, Split: row.Split})
	}
	return nil
}

func (m *Module) taskDetail(w http.ResponseWriter, r *http.Request, rawID string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	task, err := m.loadTaskDTO(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "task_not_found", Message: "No se encontró la tarea."})
		return
	}
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if task.HostID != "" {
		if _, ok := m.hostName(r.Context(), task.HostID); !ok {
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "task_not_found", Message: "No se encontró la tarea en el ámbito de hosts actual."})
			return
		}
	}
	// Sólo el detalle paga esta consulta; las listas no la pagan.
	if err := m.publishTaskCases(r.Context(), &task); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// taskHistory publishes the append-only record of one task: its completions and its
// label/feedback writes. It is a detail route on purpose, so no list pays for it.
func (m *Module) taskHistory(w http.ResponseWriter, r *http.Request, rawID string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	task, err := m.loadTaskDTO(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "task_not_found", Message: "No se encontró la tarea."})
		return
	}
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if task.HostID != "" {
		if _, ok := m.hostName(r.Context(), task.HostID); !ok {
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "task_not_found", Message: "No se encontró la tarea en el ámbito de hosts actual."})
			return
		}
	}
	completions, labels, err := m.loadTaskHistory(r.Context(), id)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task_id": id, "completions": completions, "labels": labels})
}

func (m *Module) loadTaskHistory(ctx context.Context, id string) ([]TaskCompletionEntry, []TaskLabelEntry, error) {
	completions := make([]TaskCompletionEntry, 0)
	rows, err := m.db.QueryContext(ctx, `SELECT generation,completed,confirmed_at,revoked_generation,created FROM task_completion_history WHERE task_id=? ORDER BY id`, id)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var entry TaskCompletionEntry
		var completed int
		var confirmed sql.NullFloat64
		var revoked sql.NullInt64
		if err := rows.Scan(&entry.Generation, &completed, &confirmed, &revoked, &entry.Created); err != nil {
			rows.Close()
			return nil, nil, err
		}
		entry.Completed = completed != 0
		if confirmed.Valid {
			value := confirmed.Float64
			entry.ConfirmedAt = &value
		}
		if revoked.Valid {
			value := revoked.Int64
			entry.RevokedGeneration = &value
		}
		completions = append(completions, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()
	labels := make([]TaskLabelEntry, 0)
	labelRows, err := m.db.QueryContext(ctx, `SELECT generation,action,label,feedback,created FROM task_label_history WHERE task_id=? ORDER BY id`, id)
	if err != nil {
		return nil, nil, err
	}
	defer labelRows.Close()
	for labelRows.Next() {
		var entry TaskLabelEntry
		if err := labelRows.Scan(&entry.Generation, &entry.Action, &entry.Label, &entry.Feedback, &entry.Created); err != nil {
			return nil, nil, err
		}
		labels = append(labels, entry)
	}
	return completions, labels, labelRows.Err()
}

func (m *Module) setTaskCompletion(w http.ResponseWriter, r *http.Request, rawID string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var request completionRequest
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if request.Completed == nil {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "completed debe ser booleano."})
		return
	}
	task, reused, apiErr := m.changeTaskCompletion(r.Context(), id, *request.Completed, request.ExpectedGeneration)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task, "reused": reused})
}

const taskRecordSelect = `SELECT t.id,t.session_id,t.revision,t.first_event,t.last_event,t.title,t.label,t.feedback,t.completed,t.completion_generation,t.completion_confirmed_at,t.created,t.updated,s.source_id,s.host_id,r.coverage_json FROM tasks t JOIN sessions s ON s.id=t.session_id JOIN revisions r ON r.session_id=t.session_id AND r.revision=t.revision WHERE t.id=?`

// loadTaskRecordTx loads a task inside tx and enforces the host scope shared by every
// mutation: a task of a host outside the current scope reads as not found.
func (m *Module) loadTaskRecordTx(ctx context.Context, tx *sql.Tx, id string) (taskRecord, *APIError) {
	var current taskRecord
	err := tx.QueryRowContext(ctx, taskRecordSelect, id).Scan(&current.ID, &current.SessionID, &current.Revision, &current.FirstEvent, &current.LastEvent, &current.Title, &current.Label, &current.Feedback, &current.Completed, &current.CompletionGeneration, &current.CompletionConfirmedAt, &current.Created, &current.Updated, &current.SourceID, &current.HostID, &current.CoverageJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return current, &APIError{Status: http.StatusNotFound, Code: "task_not_found", Message: "No se encontró la tarea."}
	}
	if err != nil {
		return current, internalError(err)
	}
	if apiErr := m.hostScopeError(ctx, current.HostID); apiErr != nil {
		return current, apiErr
	}
	return current, nil
}

// hostScopeError is nil when hostID is local or still inside the current host scope.
func (m *Module) hostScopeError(ctx context.Context, hostID string) *APIError {
	if hostID == "" {
		return nil
	}
	if _, ok := m.hostName(ctx, hostID); !ok {
		return &APIError{Status: http.StatusNotFound, Code: "task_not_found", Message: "No se encontró la tarea en el ámbito de hosts actual."}
	}
	return nil
}

// publishAppliedTask refreshes the task cases after a committed change. The change is
// already applied, so a failure is logged instead of turning it into a 500.
func (m *Module) publishAppliedTask(ctx context.Context, task *TaskDTO) {
	if err := m.publishTaskCases(ctx, task); err != nil {
		log.Printf("foundry: task %s changed but publishing its learning cases failed: %v", task.ID, err)
	}
}

func (m *Module) changeTaskCompletion(ctx context.Context, id string, completed bool, expected *int64) (TaskDTO, bool, *APIError) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	defer tx.Rollback()
	current, apiErr := m.loadTaskRecordTx(ctx, tx, id)
	if apiErr != nil {
		return TaskDTO{}, false, apiErr
	}
	if expected != nil && *expected != current.CompletionGeneration {
		return TaskDTO{}, false, &APIError{Status: http.StatusConflict, Code: "generation_conflict", Message: "La tarea cambió; vuelve a cargar su generación actual."}
	}
	if current.Completed == completed {
		_ = tx.Rollback()
		task, err := m.loadTaskDTO(ctx, id)
		if err != nil {
			return TaskDTO{}, false, internalError(err)
		}
		if err := m.publishTaskCases(ctx, &task); err != nil {
			return TaskDTO{}, false, internalError(err)
		}
		return task, true, nil
	}
	if completed {
		var latest string
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM sessions WHERE id=?`, current.SessionID).Scan(&latest); err != nil {
			return TaskDTO{}, false, internalError(err)
		}
		if latest != current.Revision {
			return TaskDTO{}, false, &APIError{Status: http.StatusConflict, Code: "stale_revision", Message: "No se puede completar una tarea anclada a una revisión histórica."}
		}
		var coverage CoverageSummary
		if err := json.Unmarshal([]byte(current.CoverageJSON), &coverage); err != nil || coverage.Status != "complete" {
			return TaskDTO{}, false, &APIError{Status: http.StatusConflict, Code: "coverage_incomplete", Message: "La cobertura de la revisión no permite confirmar finalización."}
		}
	}
	now := nowSeconds()
	generation := current.CompletionGeneration + 1
	label, feedback := current.Label, current.Feedback
	var confirmed any
	if completed {
		confirmed = now
	} else {
		label, feedback = "pending", ""
	}
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET completed=?,completion_generation=?,completion_confirmed_at=?,label=?,feedback=?,updated=? WHERE id=? AND completion_generation=?`, boolInt(completed), generation, confirmed, label, feedback, now, id, current.CompletionGeneration)
	if err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return TaskDTO{}, false, &APIError{Status: http.StatusConflict, Code: "generation_conflict", Message: "La tarea cambió durante la actualización."}
	}
	var revoked any
	if !completed {
		revoked = current.CompletionGeneration
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_completion_history(task_id,generation,completed,confirmed_at,revoked_generation,created) VALUES(?,?,?,?,?,?)`, id, generation, boolInt(completed), confirmed, revoked, now); err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	if !completed && (current.Label != "pending" || current.Feedback != "") {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_label_history(task_id,generation,action,label,feedback,created) VALUES(?,?,'label','pending','',?)`, id, generation, now); err != nil {
			return TaskDTO{}, false, internalError(err)
		}
	}
	action := "completion_confirmed"
	if !completed {
		action = "completion_revoked"
	}
	if err := appendAudit(ctx, tx, "task", id, action, map[string]any{"generation": generation, "revoked_generation": current.CompletionGeneration, "revision": current.Revision}); err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	if err := invalidateLearningTask(ctx, tx, id, "completion_generation_changed"); err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	if err := tx.Commit(); err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	m.cancelLearningWorkersForTask(id)
	task, err := m.loadTaskDTO(ctx, id)
	if err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	m.publishAppliedTask(ctx, &task)
	return task, false, nil
}

func (m *Module) setTaskLabel(w http.ResponseWriter, r *http.Request, rawID string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var request labelRequest
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if request.Label != "pending" && request.Label != "correct" && request.Label != "incorrect" {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_label", Message: "La etiqueta debe ser pending, correct o incorrect."})
		return
	}
	if request.Feedback != nil {
		if _, apiErr := cleanUserText(*request.Feedback, maxFeedback); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
	}
	task, reused, apiErr := m.updateTaskDecision(r.Context(), id, &request.Label, request.Feedback, request.ExpectedGeneration)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task, "reused": reused})
}

func (m *Module) setTaskFeedback(w http.ResponseWriter, r *http.Request, rawID string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var request feedbackRequest
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if request.Feedback == nil {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "feedback es obligatorio."})
		return
	}
	task, reused, apiErr := m.updateTaskDecision(r.Context(), id, nil, request.Feedback, request.ExpectedGeneration)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task, "reused": reused})
}

func (m *Module) updateTaskDecision(ctx context.Context, id string, nextLabel *string, nextFeedback *string, expected *int64) (TaskDTO, bool, *APIError) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	defer tx.Rollback()
	current, apiErr := m.loadTaskRecordTx(ctx, tx, id)
	if apiErr != nil {
		return TaskDTO{}, false, apiErr
	}
	if expected != nil && *expected != current.CompletionGeneration {
		return TaskDTO{}, false, &APIError{Status: http.StatusConflict, Code: "generation_conflict", Message: "La tarea cambió; vuelve a cargar su generación actual."}
	}
	label, feedback := current.Label, current.Feedback
	if nextLabel != nil {
		label = *nextLabel
	}
	if nextFeedback != nil {
		cleaned, apiErr := cleanUserText(*nextFeedback, maxFeedback)
		if apiErr != nil {
			return TaskDTO{}, false, apiErr
		}
		feedback = cleaned
	}
	if nextLabel != nil && *nextLabel != "pending" && !current.Completed {
		return TaskDTO{}, false, &APIError{Status: http.StatusConflict, Code: "task_incomplete", Message: "Confirma primero la finalización humana antes de valorar la corrección de la tarea."}
	}
	if label == current.Label && feedback == current.Feedback {
		tx.Rollback()
		task, err := m.loadTaskDTO(ctx, id)
		if err != nil {
			return TaskDTO{}, false, internalError(err)
		}
		if err := m.publishTaskCases(ctx, &task); err != nil {
			return TaskDTO{}, false, internalError(err)
		}
		return task, true, nil
	}
	now := nowSeconds()
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET label=?,feedback=?,updated=? WHERE id=? AND completion_generation=?`, label, feedback, now, id, current.CompletionGeneration); err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	action := "label"
	if nextLabel == nil {
		action = "feedback"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_label_history(task_id,generation,action,label,feedback,created) VALUES(?,?,?,?,?,?)`, id, current.CompletionGeneration, action, label, feedback, now); err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	if err := appendAudit(ctx, tx, "task", id, "task_"+action+"_updated", map[string]any{"generation": current.CompletionGeneration, "label": label}); err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	reason := "task_feedback_changed"
	if nextLabel != nil {
		reason = "task_label_changed"
	}
	if err := invalidateLearningTask(ctx, tx, id, reason); err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	if err := tx.Commit(); err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	m.cancelLearningWorkersForTask(id)
	task, err := m.loadTaskDTO(ctx, id)
	if err != nil {
		return TaskDTO{}, false, internalError(err)
	}
	m.publishAppliedTask(ctx, &task)
	return task, false, nil
}

func (m *Module) loadTaskDTO(ctx context.Context, id string) (TaskDTO, error) {
	var task TaskDTO
	var completed int
	var confirmed sql.NullFloat64
	var coverageJSON string
	err := m.db.QueryRowContext(ctx, `SELECT t.id,t.session_id,t.revision,t.title,t.first_event,t.last_event,t.label,t.feedback,t.completed,t.completion_generation,t.completion_confirmed_at,t.created,t.updated,r.coverage_json,s.source_id,s.host_id FROM tasks t JOIN revisions r ON r.session_id=t.session_id AND r.revision=t.revision JOIN sessions s ON s.id=t.session_id WHERE t.id=?`, id).Scan(&task.ID, &task.SessionID, &task.Revision, &task.Title, &task.FirstEvent, &task.LastEvent, &task.Label, &task.Feedback, &completed, &task.CompletionGeneration, &confirmed, &task.Created, &task.Updated, &coverageJSON, &task.SourceID, &task.HostID)
	if err != nil {
		return task, err
	}
	task.Completed = completed != 0
	if task.Completed {
		task.Status = task.Label
	} else {
		task.Status = "incomplete"
	}
	if confirmed.Valid {
		value := confirmed.Float64
		task.CompletionConfirmedAt = &value
	}
	if err := json.Unmarshal([]byte(coverageJSON), &task.Coverage); err != nil {
		task.Coverage = CoverageSummary{Status: "unknown"}
	}
	rows, err := m.db.QueryContext(ctx, `SELECT event_index,quote FROM task_citations WHERE task_id=? ORDER BY citation_index`, id)
	if err != nil {
		return task, err
	}
	defer rows.Close()
	task.Citations = make([]TaskCitation, 0)
	for rows.Next() {
		var citation TaskCitation
		if err := rows.Scan(&citation.EventIndex, &citation.Quote); err != nil {
			return task, err
		}
		task.Citations = append(task.Citations, citation)
	}
	return task, rows.Err()
}

func cleanUserText(value string, maxRunes int) (string, *APIError) {
	if !utf8.ValidString(value) {
		return "", &APIError{Status: http.StatusBadRequest, Code: "invalid_text", Message: "El texto no es UTF-8 válido."}
	}
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > maxRunes {
		return "", &APIError{Status: http.StatusBadRequest, Code: "text_too_long", Message: "El texto supera el límite permitido."}
	}
	for _, char := range value {
		if char < 32 && char != '\n' && char != '\r' && char != '\t' {
			return "", &APIError{Status: http.StatusBadRequest, Code: "invalid_text", Message: "El texto contiene controles no permitidos."}
		}
	}
	cleaned, _, err := redactString(value, false)
	if err != nil {
		return "", &APIError{Status: http.StatusBadRequest, Code: "invalid_text", Message: "El texto no se puede guardar de forma segura."}
	}
	return strings.TrimSpace(cleaned), nil
}

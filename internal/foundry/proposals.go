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
	"time"
)

const taskProposalAlgorithm = "semantic-tasks-v1"

type TaskSuggestion struct {
	Title      string         `json:"title"`
	FirstEvent int            `json:"first_event"`
	LastEvent  int            `json:"last_event"`
	Evidence   []TaskCitation `json:"evidence"`
}

type TaskProposalProgress struct {
	CompletedChunks int `json:"completed_chunks"`
	TotalChunks     int `json:"total_chunks"`
}

type ProposalSkipped struct {
	FirstEvent int      `json:"first_event"`
	LastEvent  int      `json:"last_event"`
	Reason     string   `json:"reason"`
	TaskIDs    []string `json:"task_ids,omitempty"`
}

type ProposalInference struct {
	Calls   int                  `json:"calls"`
	Seconds float64              `json:"seconds"`
	Runs    []ProposalRunReceipt `json:"runs"`
}

type ProposalRunReceipt struct {
	Provider      string         `json:"provider"`
	Model         string         `json:"model"`
	ModelProvider string         `json:"model_provider,omitempty"`
	Status        string         `json:"status"`
	Error         string         `json:"error,omitempty"`
	Usage         map[string]int `json:"usage,omitempty"`
	Seconds       float64        `json:"seconds"`
	Turns         *int           `json:"turns,omitempty"`
	ToolCalls     *int           `json:"tool_calls,omitempty"`
	RealInference bool           `json:"real_inference"`
	Command       []string       `json:"command"`
}

type TaskProposalDTO struct {
	JobID       string               `json:"job_id"`
	SessionID   string               `json:"session_id"`
	Revision    string               `json:"revision"`
	Algorithm   string               `json:"algorithm"`
	Automatic   bool                 `json:"automatic"`
	Status      string               `json:"status"`
	Suggestions []TaskSuggestion     `json:"suggestions"`
	Skipped     []ProposalSkipped    `json:"skipped"`
	Coverage    map[string]any       `json:"coverage"`
	Inference   ProposalInference    `json:"inference"`
	Progress    TaskProposalProgress `json:"progress"`
	Error       string               `json:"error,omitempty"`
	StartedAt   float64              `json:"started_at"`
	UpdatedAt   float64              `json:"updated_at"`
	FinishedAt  *float64             `json:"finished_at,omitempty"`
}

type proposalAcceptRequest struct {
	ExpectedRevision  string `json:"expected_revision"`
	SuggestionIndices []int  `json:"suggestion_indices"`
}

func (m *Module) taskProposal(w http.ResponseWriter, r *http.Request, rawSessionID string) {
	sessionID, apiErr := pathValue(rawSessionID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	revision := r.URL.Query().Get("revision")
	if revision == "" {
		if err := m.db.QueryRowContext(r.Context(), `SELECT revision FROM sessions WHERE id=?`, sessionID).Scan(&revision); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "session_not_found", Message: "No se encontró la conversación."})
			} else {
				writeAPIError(w, internalError(err))
			}
			return
		}
	}
	proposal, err := m.loadTaskProposal(r.Context(), sessionID, revision)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "proposal_not_found", Message: "No hay propuesta para esa revisión."})
		return
	}
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (m *Module) enqueueTaskProposal(ctx context.Context, sessionID, revision string, automatic bool) (string, *APIError) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", &APIError{Status: http.StatusServiceUnavailable, Code: "closed", Message: "Foundry se está cerrando."}
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return "", internalError(err)
	}
	defer tx.Rollback()
	cfg, err := loadTaskConfig(ctx, tx)
	if err != nil {
		return "", internalError(err)
	}
	var currentRevision, coverageJSON string
	var updated, activity float64
	var enabled int
	err = tx.QueryRowContext(ctx, `SELECT s.revision,s.coverage_json,s.updated,s.activity_at,src.enabled FROM sessions s JOIN sources src ON src.id=s.source_id WHERE s.id=?`, sessionID).Scan(&currentRevision, &coverageJSON, &updated, &activity, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &APIError{Status: http.StatusNotFound, Code: "session_not_found", Message: "No se encontró la conversación."}
	}
	if err != nil {
		return "", internalError(err)
	}
	if currentRevision != revision {
		return "", &APIError{Status: http.StatusConflict, Code: "stale_revision", Message: "La revisión visible cambió; vuelve a abrir la conversación."}
	}
	var coverage CoverageSummary
	if json.Unmarshal([]byte(coverageJSON), &coverage) != nil || coverage.Status != "complete" || coverage.AppendPending {
		return "", &APIError{Status: http.StatusConflict, Code: "incomplete_coverage", Message: "La cobertura de esta revisión no está completa; no se proponen tareas."}
	}
	if enabled == 0 {
		return "", &APIError{Status: http.StatusConflict, Code: "source_revoked", Message: "La fuente está desactivada."}
	}
	if automatic {
		if !cfg.AutoTasks {
			return "", &APIError{Status: http.StatusConflict, Code: "automatic_disabled", Message: "Las propuestas automáticas están desactivadas."}
		}
		var since sql.NullFloat64
		if err := tx.QueryRowContext(ctx, `SELECT auto_tasks_since FROM task_settings WHERE singleton=1`).Scan(&since); err != nil {
			return "", internalError(err)
		}
		activityAt := coverage.ActivityAt
		if activityAt == 0 {
			activityAt = activity
		}
		if !since.Valid || activityAt < since.Float64 || nowSeconds()-max(activityAt, updated) < float64(cfg.TaskIdleSeconds) {
			return "", &APIError{Status: http.StatusConflict, Code: "not_quiet", Message: "La conversación aún no cumple el periodo de inactividad consentido."}
		}
	}
	if !m.providerDiagnostics(cfg).Provider.Available {
		return "", &APIError{Status: http.StatusConflict, Code: "provider_unavailable", Message: "El proveedor de análisis no está disponible con las garantías configuradas."}
	}
	providerHash := hashJSON(cfg.Provider)
	identity := hashJSON(map[string]any{"algorithm": taskProposalAlgorithm, "session_id": sessionID, "revision": revision, "provider": cfg.Provider, "max_input_chars": cfg.MaxInputChars})
	var previousID, previousStatus string
	err = tx.QueryRowContext(ctx, `SELECT job_id,status FROM task_proposal_receipts WHERE session_id=? AND revision=? ORDER BY created DESC LIMIT 1`, sessionID, revision).Scan(&previousID, &previousStatus)
	if err == nil {
		if automatic {
			return "", &APIError{Status: http.StatusConflict, Code: "automatic_already_attempted", Message: "Esta revisión ya tuvo un intento automático; no se reintenta sin una acción humana."}
		}
		if previousStatus == "queued" || previousStatus == "running" || previousStatus == "completed" {
			return previousID, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", internalError(err)
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='propose_tasks' AND status IN ('queued','running')`).Scan(&active); err != nil {
		return "", internalError(err)
	}
	if active > 0 {
		return "", &APIError{Status: http.StatusConflict, Code: "proposal_in_progress", Message: "Ya hay una propuesta de tareas en curso."}
	}
	jobID, err := newID("taskproposal_")
	if err != nil {
		return "", internalError(err)
	}
	attempt, err := newID("attempt_")
	if err != nil {
		return "", internalError(err)
	}
	now := nowSeconds()
	coverageReceipt := map[string]any{"status": coverage.Status, "event_count": coverage.EventCount, "issue_count": coverage.IssueCount, "blocking_issue_count": coverage.BlockingIssueCount, "raw_sha256": coverage.RawSHA256}
	coverageText := mustJSON(coverageReceipt)
	inferenceText := mustJSON(ProposalInference{Runs: []ProposalRunReceipt{}})
	if _, err := tx.ExecContext(ctx, `INSERT INTO jobs(id,kind,subject,status,created,updated,progress_json) VALUES(?,'propose_tasks',?,'queued',?,?,?)`, jobID, sessionID, now, now, mustJSON(TaskProposalProgress{})); err != nil {
		return "", internalError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_proposal_receipts(job_id,session_id,revision,algorithm,provider_hash,identity_hash,automatic,status,attempt,started_at,lease_until,suggestions_json,skipped_json,coverage_json,inference_json,error,progress_completed,progress_total,created,updated) VALUES(?,?,?,?,?,?,?,'queued',?,?,?,?,?,?,?,'',0,0,?,?)`, jobID, sessionID, revision, taskProposalAlgorithm, providerHash, identity, boolInt(automatic), attempt, now, now+cfg.ExperimentSeconds, `[]`, `[]`, coverageText, inferenceText, now, now); err != nil {
		return "", internalError(err)
	}
	if err := tx.Commit(); err != nil {
		return "", internalError(err)
	}
	jobCtx, cancel := context.WithCancel(m.closeCtx)
	m.jobs[jobID] = cancel
	m.wg.Add(1)
	go m.runTaskProposalJob(jobCtx, jobID, sessionID, revision, automatic, cancel)
	return jobID, nil
}

func (m *Module) runTaskProposalJob(ctx context.Context, jobID, sessionID, revision string, automatic bool, cancel context.CancelFunc) {
	defer m.wg.Done()
	defer cancel()
	defer m.forgetJob(jobID)
	_, _ = m.db.ExecContext(context.Background(), `UPDATE jobs SET status='running',updated=? WHERE id=? AND status='queued'`, nowSeconds(), jobID)
	_, _ = m.db.ExecContext(context.Background(), `UPDATE task_proposal_receipts SET status='running',updated=? WHERE job_id=? AND status='queued'`, nowSeconds(), jobID)
	result, err := m.proposeTasks(ctx, jobID, sessionID, revision, automatic)
	status, jobStatus, message := "completed", "succeeded", ""
	if err != nil {
		message = err.Error()
		status, jobStatus = "failed", "failed"
		if ctx.Err() != nil {
			status, jobStatus, message = "cancelled", "cancelled", "cancelled"
		}
		var current string
		if scanErr := m.db.QueryRowContext(context.Background(), `SELECT revision FROM sessions WHERE id=?`, sessionID).Scan(&current); scanErr != nil || current != revision {
			status, message = "stale", "La conversación tiene una revisión nueva; vuelve a proponer sobre la captura actual."
		}
	}
	finished := nowSeconds()
	if err := m.finalizeProposalJob(jobID, sessionID, revision, automatic, status, jobStatus, message, finished, result, err == nil); err != nil {
		log.Printf("foundry: finalising task proposal job %s failed: %v", jobID, err)
	}
	m.notifyTaskScheduler()
}

// finalizeProposalJob writes the receipt and the job row in one transaction so they
// can never disagree about the outcome.
func (m *Module) finalizeProposalJob(jobID, sessionID, revision string, automatic bool, status, jobStatus, message string, finished float64, result proposalWorkResult, ok bool) error {
	ctx := context.Background()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if ok {
		progressText := mustJSON(result.Progress)
		if _, err := tx.ExecContext(ctx, `UPDATE task_proposal_receipts SET status=?,suggestions_json=?,skipped_json=?,coverage_json=?,inference_json=?,error='',progress_completed=?,progress_total=?,finished_at=?,updated=? WHERE job_id=?`, status, mustJSON(result.Suggestions), mustJSON(result.Skipped), mustJSON(result.Coverage), mustJSON(result.Inference), result.Progress.CompletedChunks, result.Progress.TotalChunks, finished, finished, jobID); err != nil {
			return err
		}
		payload := TaskProposalDTO{JobID: jobID, SessionID: sessionID, Revision: revision, Algorithm: taskProposalAlgorithm, Automatic: automatic, Status: status, Suggestions: result.Suggestions, Skipped: result.Skipped, Coverage: result.Coverage, Inference: result.Inference, Progress: result.Progress, StartedAt: result.StartedAt, UpdatedAt: finished, FinishedAt: new(finished)}
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status=?,result_json=?,error='',progress_json=?,updated=? WHERE id=?`, jobStatus, mustJSON(payload), progressText, finished, jobID); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, "task_proposal", jobID, "task_proposals_completed", map[string]any{"session_id": sessionID, "revision": revision, "suggestions": len(result.Suggestions), "automatic": automatic}); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE task_proposal_receipts SET status=?,error=?,finished_at=?,updated=? WHERE job_id=?`, status, message, finished, finished, jobID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status=?,error=?,updated=? WHERE id=?`, jobStatus, message, finished, jobID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Module) loadTaskProposal(ctx context.Context, sessionID, revision string) (TaskProposalDTO, error) {
	var proposal TaskProposalDTO
	var automatic int
	var suggestions, skipped, coverage, inference string
	var finished sql.NullFloat64
	err := m.db.QueryRowContext(ctx, `SELECT job_id,session_id,revision,algorithm,automatic,status,suggestions_json,skipped_json,coverage_json,inference_json,error,progress_completed,progress_total,started_at,updated,finished_at FROM task_proposal_receipts WHERE session_id=? AND revision=? ORDER BY created DESC LIMIT 1`, sessionID, revision).Scan(&proposal.JobID, &proposal.SessionID, &proposal.Revision, &proposal.Algorithm, &automatic, &proposal.Status, &suggestions, &skipped, &coverage, &inference, &proposal.Error, &proposal.Progress.CompletedChunks, &proposal.Progress.TotalChunks, &proposal.StartedAt, &proposal.UpdatedAt, &finished)
	if err != nil {
		return TaskProposalDTO{}, err
	}
	proposal.Automatic = automatic != 0
	proposal.FinishedAt = nullFloat(finished)
	proposal.Suggestions = make([]TaskSuggestion, 0)
	proposal.Skipped = make([]ProposalSkipped, 0)
	proposal.Coverage = map[string]any{}
	_ = json.Unmarshal([]byte(suggestions), &proposal.Suggestions)
	_ = json.Unmarshal([]byte(skipped), &proposal.Skipped)
	_ = json.Unmarshal([]byte(coverage), &proposal.Coverage)
	_ = json.Unmarshal([]byte(inference), &proposal.Inference)
	if proposal.Inference.Runs == nil {
		proposal.Inference.Runs = []ProposalRunReceipt{}
	}
	return proposal, nil
}

func (m *Module) acceptTaskProposal(w http.ResponseWriter, r *http.Request, rawJobID string) {
	jobID, apiErr := pathValue(rawJobID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var request proposalAcceptRequest
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if request.ExpectedRevision == "" || len(request.SuggestionIndices) == 0 {
		writeAPIError(w, invalidProposal("expected_revision y al menos un suggestion_index son obligatorios."))
		return
	}
	revision, apiErr := pathValue(request.ExpectedRevision)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	indices := append([]int(nil), request.SuggestionIndices...)
	sort.Ints(indices)
	for i, index := range indices {
		if index < 0 || i > 0 && indices[i-1] == index {
			writeAPIError(w, invalidProposal("Los suggestion_indices deben ser únicos y no negativos."))
			return
		}
	}
	tasks, skipped, apiErr := m.acceptTaskSuggestions(r.Context(), jobID, revision, indices)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks, "skipped": skipped})
}

func (m *Module) acceptTaskSuggestions(ctx context.Context, jobID, expectedRevision string, indices []int) ([]TaskDTO, []ProposalSkipped, *APIError) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, internalError(err)
	}
	defer tx.Rollback()
	var sessionID, revision, status, suggestionsText, coverageText, inferenceText string
	err = tx.QueryRowContext(ctx, `SELECT session_id,revision,status,suggestions_json,coverage_json,inference_json FROM task_proposal_receipts WHERE job_id=?`, jobID).Scan(&sessionID, &revision, &status, &suggestionsText, &coverageText, &inferenceText)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, &APIError{Status: http.StatusNotFound, Code: "proposal_not_found", Message: "No se encontró la propuesta."}
	}
	if err != nil {
		return nil, nil, internalError(err)
	}
	if status != "completed" {
		return nil, nil, &APIError{Status: http.StatusConflict, Code: "proposal_not_ready", Message: "La propuesta no está completa para aceptarse."}
	}
	if revision != expectedRevision {
		return nil, nil, &APIError{Status: http.StatusConflict, Code: "stale_revision", Message: "La propuesta pertenece a otra revisión."}
	}
	var current, hostID string
	var sourceEnabled int
	if err := tx.QueryRowContext(ctx, `SELECT s.revision,src.enabled,s.host_id FROM sessions s JOIN sources src ON src.id=s.source_id WHERE s.id=?`, sessionID).Scan(&current, &sourceEnabled, &hostID); err != nil || current != revision {
		return nil, nil, &APIError{Status: http.StatusConflict, Code: "stale_revision", Message: "La conversación cambió; la propuesta ya no se puede aceptar."}
	}
	if sourceEnabled == 0 {
		return nil, nil, &APIError{Status: http.StatusConflict, Code: "source_revoked", Message: "La fuente está desactivada; la propuesta ya no se puede aceptar."}
	}
	if apiErr := m.hostScopeError(ctx, hostID); apiErr != nil {
		return nil, nil, apiErr
	}
	var suggestions []TaskSuggestion
	if err := json.Unmarshal([]byte(suggestionsText), &suggestions); err != nil {
		return nil, nil, internalError(err)
	}
	var coverage CoverageSummary
	if json.Unmarshal([]byte(coverageText), &coverage) != nil || coverage.Status != "complete" || coverage.AppendPending {
		return nil, nil, &APIError{Status: http.StatusConflict, Code: "incomplete_coverage", Message: "La cobertura de la propuesta no es completa."}
	}
	var inference ProposalInference
	if err := json.Unmarshal([]byte(inferenceText), &inference); err != nil {
		return nil, nil, internalError(errors.New("invalid inference receipt"))
	}
	real := false
	for _, run := range inference.Runs {
		if run.RealInference {
			real = true
			break
		}
	}
	if !real {
		return nil, nil, &APIError{Status: http.StatusConflict, Code: "provider_receipt_missing", Message: "La propuesta no tiene recibo verificable de inferencia real."}
	}
	var events []proposalEvent
	if err := loadProposalEvents(ctx, tx, sessionID, revision, &events); err != nil {
		return nil, nil, internalError(err)
	}
	if err := validateTaskSuggestions(suggestions, events, nil); err != nil {
		return nil, nil, invalidProposal(err.Error())
	}
	for _, index := range indices {
		if index >= len(suggestions) {
			return nil, nil, invalidProposal("suggestion_index fuera del rango publicado.")
		}
	}
	createdIDs := make([]string, 0, len(indices))
	skipped := make([]ProposalSkipped, 0)
	for _, index := range indices {
		suggestion := suggestions[index]
		var decisionCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_proposal_decisions WHERE job_id=? AND suggestion_index=?`, jobID, index).Scan(&decisionCount); err != nil {
			return nil, nil, internalError(err)
		}
		if decisionCount > 0 {
			return nil, nil, &APIError{Status: http.StatusConflict, Code: "proposal_already_decided", Message: "Una o más sugerencias ya tuvieron una decisión humana."}
		}
		var overlaps int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE session_id=? AND revision=? AND NOT(last_event<? OR first_event>?)`, sessionID, revision, suggestion.FirstEvent, suggestion.LastEvent).Scan(&overlaps); err != nil {
			return nil, nil, internalError(err)
		}
		if overlaps > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_proposal_decisions(job_id,suggestion_index,action,task_id,reason,created) VALUES(?,?,'skipped',NULL,'already_represented',?)`, jobID, index, nowSeconds()); err != nil {
				return nil, nil, internalError(err)
			}
			skipped = append(skipped, ProposalSkipped{FirstEvent: suggestion.FirstEvent, LastEvent: suggestion.LastEvent, Reason: "already_represented"})
			continue
		}
		taskID, err := newID("task_")
		if err != nil {
			return nil, nil, internalError(err)
		}
		now := nowSeconds()
		if _, err := tx.ExecContext(ctx, `INSERT INTO tasks(id,session_id,revision,first_event,last_event,title,label,feedback,completed,completion_generation,completion_confirmed_at,created,updated) VALUES(?,?,?,?,?,?,'pending','',0,0,NULL,?,?)`, taskID, sessionID, revision, suggestion.FirstEvent, suggestion.LastEvent, suggestion.Title, now, now); err != nil {
			return nil, nil, internalError(err)
		}
		for citationIndex, citation := range suggestion.Evidence {
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_citations(task_id,citation_index,event_index,quote) VALUES(?,?,?,?)`, taskID, citationIndex, citation.EventIndex, citation.Quote); err != nil {
				return nil, nil, internalError(err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_proposal_decisions(job_id,suggestion_index,action,task_id,reason,created) VALUES(?,?,'accepted',?,'',?)`, jobID, index, taskID, now); err != nil {
			return nil, nil, internalError(err)
		}
		if err := appendAudit(ctx, tx, "task", taskID, "task_accepted_from_proposal", map[string]any{"job_id": jobID, "session_id": sessionID, "revision": revision, "suggestion_index": index}); err != nil {
			return nil, nil, internalError(err)
		}
		createdIDs = append(createdIDs, taskID)
	}
	if err := appendAudit(ctx, tx, "task_proposal", jobID, "task_proposal_decisions", map[string]any{"accepted": len(createdIDs), "skipped": len(skipped)}); err != nil {
		return nil, nil, internalError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, internalError(err)
	}
	created := make([]TaskDTO, 0, len(createdIDs))
	for _, taskID := range createdIDs {
		task, err := m.loadTaskDTO(ctx, taskID)
		if err != nil {
			return nil, nil, internalError(err)
		}
		created = append(created, task)
	}
	return created, skipped, nil
}

func invalidProposal(message string) *APIError {
	return &APIError{Status: http.StatusBadRequest, Code: "invalid_proposal", Message: message}
}

type proposalWorkResult struct {
	Suggestions []TaskSuggestion
	Skipped     []ProposalSkipped
	Coverage    map[string]any
	Inference   ProposalInference
	Progress    TaskProposalProgress
	StartedAt   float64
}

var _ = time.Second

// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// LearningProgress is bounded, non-sensitive job progress. Transcript text and prompts never belong here.
type LearningProgress struct {
	Message   string             `json:"message,omitempty"`
	Phase     string             `json:"phase,omitempty"`
	Percent   int                `json:"percent,omitempty"`
	Coverage  string             `json:"coverage,omitempty"`
	Truncated bool               `json:"truncated,omitempty"`
	Chunks    LearningChunkState `json:"chunks,omitempty"`
}

type LearningChunkState struct {
	Completed   int                    `json:"completed"`
	Total       int                    `json:"total"`
	Invalidated []LearningInvalidation `json:"invalidated,omitempty"`
}

type LearningInvalidation struct {
	Reason string `json:"reason"`
	Phase  string `json:"phase,omitempty"`
}

type LearningJobDTO struct {
	JobID    string           `json:"job_id"`
	Kind     string           `json:"kind"`
	Subject  string           `json:"subject"`
	Status   string           `json:"status"`
	Progress LearningProgress `json:"progress"`
	Result   map[string]any   `json:"result,omitempty"`
	Error    string           `json:"error,omitempty"`
	Created  float64          `json:"created"`
	Updated  float64          `json:"updated"`
}

type CandidateDTO struct {
	ID          string         `json:"id"`
	EpisodeID   string         `json:"episode_id"`
	TaskID      string         `json:"task_id,omitempty"`
	SessionID   string         `json:"session_id"`
	Revision    string         `json:"revision"`
	Status      string         `json:"status"`
	BaseVersion *string        `json:"base_version"`
	Created     float64        `json:"created"`
	Spec        map[string]any `json:"spec"`
	Stale       bool           `json:"stale"`
	StaleReason string         `json:"stale_reason,omitempty"`
}

func (m *Module) serveLearning(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 1 && parts[0] == "cases" {
		switch r.Method {
		case http.MethodGet:
			cases, err := m.listLearningCases(r.Context())
			if err != nil {
				writeAPIError(w, internalError(err))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"cases": cases})
		case http.MethodPost:
			m.queueLearningCase(w, r)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(parts) == 1 && parts[0] == "backup" {
		m.createLearningBackup(w, r)
		return
	}
	if len(parts) == 2 && parts[0] == "backup" {
		m.learningBackupDownload(w, r, parts[1])
		return
	}
	if len(parts) == 2 && parts[0] == "learning" && parts[1] == "state" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		m.learningState(w, r)
		return
	}
	if len(parts) == 3 && parts[0] == "tasks" && parts[2] == "derive" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		legacyInferenceGone(w)
		return
	}
	if len(parts) == 3 && parts[0] == "tasks" && parts[2] == "cases" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		id, apiErr := pathValue(parts[1])
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		m.setTaskCases(w, r, id)
		return
	}
	if len(parts) == 3 && parts[0] == "tasks" && parts[2] == "origin" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		id, apiErr := pathValue(parts[1])
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		m.confirmLearningOrigin(w, r, id)
		return
	}
	if len(parts) == 3 && parts[0] == "candidates" {
		id, apiErr := pathValue(parts[1])
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		switch parts[2] {
		case "readiness":
			m.candidateReadinessRoute(w, r, id)
		case "scope":
			if r.Method != http.MethodPost {
				methodNotAllowed(w, http.MethodPost)
				return
			}
			m.setCandidateScope(w, r, id)
		case "setup":
			if r.Method != http.MethodPost {
				methodNotAllowed(w, http.MethodPost)
				return
			}
			m.setCandidateCases(w, r, id)
		case "evaluate":
			if r.Method != http.MethodPost {
				methodNotAllowed(w, http.MethodPost)
				return
			}
			legacyInferenceGone(w)
		case "promote":
			if r.Method != http.MethodPost {
				methodNotAllowed(w, http.MethodPost)
				return
			}
			writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "promotion_not_ready", Message: "La adopción requiere un experimento real superado y origen atestado."})
		case "files":
			if r.Method != http.MethodGet {
				methodNotAllowed(w, http.MethodGet)
				return
			}
			m.candidatePackage(w, r, id)
		default:
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Ruta no encontrada."})
		}
		return
	}
	if len(parts) == 2 && parts[0] == "candidates" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		m.learningCandidateDetail(w, r, parts[1])
		return
	}
	writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Ruta no encontrada."})
}

func (m *Module) learningState(w http.ResponseWriter, r *http.Request) {
	compact := r.URL.Query().Get("compact") == "1"
	candidates, err := m.listLearningCandidates(r.Context(), compact)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	cases, err := m.listLearningCases(r.Context())
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	experiments, err := m.listLearningExperiments(r.Context())
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	versions, active, err := m.listLearningVersions(r.Context())
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	jobs, err := m.listLearningJobs(r.Context(), 500, compact)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": candidates, "cases": cases, "experiments": experiments, "versions": versions, "active_version": active, "jobs": jobs, "limits": map[string]any{"list_rows": 500, "state_compact": compact}})
}

func (m *Module) listLearningCandidates(ctx context.Context, compact bool) ([]CandidateDTO, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT c.id,c.task_id,c.spec_json,c.status,c.base_version,c.created,t.session_id,t.revision,t.label,t.feedback,t.completed,t.completion_generation,t.first_event,t.last_event,src.enabled,r.coverage_json,s.host_id FROM candidates c JOIN tasks t ON t.id=c.task_id JOIN sessions s ON s.id=t.session_id JOIN sources src ON src.id=s.source_id JOIN revisions r ON r.session_id=t.session_id AND r.revision=t.revision ORDER BY c.created DESC,c.id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// The single pooled connection stays busy while these rows are open, so the row set
	// is drained first: loadLearningTask below runs its own query and would otherwise
	// wait on a connection that only closes at the end of this function.
	type candidateRow struct {
		candidate                       CandidateDTO
		raw, hostID, label, feedback    string
		coverage                        string
		completed, enabled, first, last int
		generation                      int64
	}
	pending := make([]candidateRow, 0)
	for rows.Next() {
		var row candidateRow
		var base sql.NullString
		if err := rows.Scan(&row.candidate.ID, &row.candidate.EpisodeID, &row.raw, &row.candidate.Status, &base, &row.candidate.Created, &row.candidate.SessionID, &row.candidate.Revision, &row.label, &row.feedback, &row.completed, &row.generation, &row.first, &row.last, &row.enabled, &row.coverage, &row.hostID); err != nil {
			return nil, err
		}
		if base.Valid {
			value := base.String
			row.candidate.BaseVersion = &value
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	out := make([]CandidateDTO, 0, len(pending))
	for _, row := range pending {
		candidate := row.candidate
		if row.hostID != "" {
			if _, ok := m.hostName(ctx, row.hostID); !ok {
				continue
			}
		}
		if err := json.Unmarshal([]byte(row.raw), &candidate.Spec); err != nil {
			return nil, err
		}
		candidate.TaskID = candidate.EpisodeID
		task, err := loadLearningTask(ctx, m.db, candidate.TaskID)
		if err != nil {
			return nil, err
		}
		candidate.Stale, candidate.StaleReason = learningCandidateStale(candidate.Spec, candidate.Status, task)
		if compact {
			candidate.Spec = compactCandidateSpec(candidate.Spec)
		}
		out = append(out, candidate)
	}
	return out, nil
}

func compactCandidateSpec(spec map[string]any) map[string]any {
	copy := make(map[string]any, len(spec))
	for key, value := range spec {
		if key != "positive" && key != "preventive" && key != "provider_evidence" && key != "analysis_coverage" {
			copy[key] = value
		}
	}
	return copy
}

func (m *Module) listLearningCases(ctx context.Context) ([]map[string]any, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id,title,domain,split,snapshot_json,verify_json,positive_control_json,negative_control_json,created FROM cases ORDER BY created DESC,id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, title, domain, split, snapshot, verify, positive, negative string
		var created float64
		if err := rows.Scan(&id, &title, &domain, &split, &snapshot, &verify, &positive, &negative, &created); err != nil {
			return nil, err
		}
		var snap any
		var verifyArgs, positiveArgs, negativeArgs []string
		if err := json.Unmarshal([]byte(snapshot), &snap); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(verify), &verifyArgs)
		_ = json.Unmarshal([]byte(positive), &positiveArgs)
		_ = json.Unmarshal([]byte(negative), &negativeArgs)
		controlsVerified, positiveStatus, negativeStatus := learningCaseControlsVerified(snapshot)
		out = append(out, map[string]any{"id": id, "title": title, "domain": domain, "split": split, "snapshot": snap, "verify": verifyArgs, "positive_control": positiveArgs, "negative_control": negativeArgs, "controls_verified": controlsVerified, "positive_control_status": positiveStatus, "negative_control_status": negativeStatus, "created": created})
	}
	return out, rows.Err()
}

func (m *Module) listLearningExperiments(ctx context.Context) ([]map[string]any, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id,candidate_id,base_version,report_json,status,created FROM experiments ORDER BY created DESC,id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, candidate, base, report, status string
		var created float64
		if err := rows.Scan(&id, &candidate, &base, &report, &status, &created); err != nil {
			return nil, err
		}
		var decoded any
		if err := json.Unmarshal([]byte(report), &decoded); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "candidate_id": candidate, "base_version": base, "report": decoded, "status": status, "created": created})
	}
	return out, rows.Err()
}

func (m *Module) listLearningVersions(ctx context.Context) ([]map[string]any, string, error) {
	active := "initial"
	if err := m.db.QueryRowContext(ctx, `SELECT active_version FROM learning_registry_state WHERE singleton=1`).Scan(&active); err != nil {
		return nil, "", err
	}
	rows, err := m.db.QueryContext(ctx, `SELECT id,parent,members_json,created,reason,package_hash FROM versions ORDER BY created,id LIMIT 500`)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, membersText, reason, packageHash string
		var parent sql.NullString
		var created float64
		if err := rows.Scan(&id, &parent, &membersText, &created, &reason, &packageHash); err != nil {
			return nil, "", err
		}
		var members any
		if err := json.Unmarshal([]byte(membersText), &members); err != nil {
			return nil, "", err
		}
		var parentValue any
		if parent.Valid {
			parentValue = parent.String
		}
		out = append(out, map[string]any{"id": id, "parent": parentValue, "members": members, "created": created, "reason": reason, "package_hash": packageHash, "verified": id == "initial"})
	}
	return out, active, rows.Err()
}

func (m *Module) listLearningJobs(ctx context.Context, limit int, compact bool) ([]LearningJobDTO, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id,kind,subject,status,progress_json,result_json,error,created,updated FROM jobs ORDER BY created DESC,id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LearningJobDTO, 0)
	for rows.Next() {
		var job LearningJobDTO
		var progress string
		var result sql.NullString
		if err := rows.Scan(&job.JobID, &job.Kind, &job.Subject, &job.Status, &progress, &result, &job.Error, &job.Created, &job.Updated); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(progress), &job.Progress) != nil {
			job.Progress.Message = truncateRunes(progress, 500)
		}
		if result.Valid && (!compact || job.Kind == "derive" || job.Kind == "case" || job.Kind == "evaluate") {
			var decoded map[string]any
			if json.Unmarshal([]byte(result.String), &decoded) == nil {
				job.Result = decoded
			}
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (m *Module) learningCandidateDetail(w http.ResponseWriter, r *http.Request, rawID string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var taskID, raw, status string
	var base sql.NullString
	var created float64
	err := m.db.QueryRowContext(r.Context(), `SELECT task_id,spec_json,status,base_version,created FROM candidates WHERE id=?`, id).Scan(&taskID, &raw, &status, &base, &created)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "candidate_not_found", Message: "No se encontró el aprendizaje."})
		return
	}
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	task, err := loadLearningTask(r.Context(), m.db, taskID)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if task.HostID != "" {
		if _, ok := m.hostName(r.Context(), task.HostID); !ok {
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "candidate_not_found", Message: "No se encontró el aprendizaje en el ámbito de hosts actual."})
			return
		}
	}
	var spec map[string]any
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	candidate := candidateDTO(id, taskID, task, status, base, created, spec)
	origin, err := m.latestLearningOrigin(r.Context(), taskID)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidate": candidate, "origin": origin})
}

func candidateDTO(id, taskID string, task learningTask, status string, base sql.NullString, created float64, spec map[string]any) CandidateDTO {
	candidate := CandidateDTO{ID: id, EpisodeID: taskID, TaskID: taskID, SessionID: task.SessionID, Revision: task.Revision, Status: status, Created: created, Spec: spec}
	if base.Valid {
		value := base.String
		candidate.BaseVersion = &value
	}
	candidate.Stale, candidate.StaleReason = learningCandidateStale(spec, status, task)
	return candidate
}

func (m *Module) learningJobByID(ctx context.Context, id string) (LearningJobDTO, bool, error) {
	var job LearningJobDTO
	var progress string
	var result sql.NullString
	err := m.db.QueryRowContext(ctx, `SELECT id,kind,subject,status,progress_json,result_json,error,created,updated FROM jobs WHERE id=?`, id).Scan(&job.JobID, &job.Kind, &job.Subject, &job.Status, &progress, &result, &job.Error, &job.Created, &job.Updated)
	if err != nil {
		return job, false, err
	}
	if job.Kind != "derive" && job.Kind != "case" && job.Kind != "evaluate" {
		return job, false, nil
	}
	if json.Unmarshal([]byte(progress), &job.Progress) != nil {
		job.Progress.Message = truncateRunes(progress, 500)
	}
	if result.Valid {
		_ = json.Unmarshal([]byte(result.String), &job.Result)
	}
	return job, true, nil
}

// foundryJobRoute handles M06 job operations; false leaves M05 scan/proposal jobs to their existing handlers.
func (m *Module) foundryJobRoute(w http.ResponseWriter, r *http.Request, id, action string) bool {
	if action == "" && r.Method == http.MethodGet {
		job, handled, err := m.learningJobByID(r.Context(), id)
		if !handled {
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				writeAPIError(w, internalError(err))
				return true
			}
			return false
		}
		writeJSON(w, http.StatusOK, job)
		return true
	}
	if action == "cancel" && r.Method == http.MethodPost {
		var kind string
		err := m.db.QueryRowContext(r.Context(), `SELECT kind FROM jobs WHERE id=?`, id).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		if err != nil {
			writeAPIError(w, internalError(err))
			return true
		}
		if kind != "derive" && kind != "case" && kind != "evaluate" {
			return false
		}
		cancelled, apiErr := m.cancelLearningJob(r.Context(), id)
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": cancelled})
		return true
	}
	if action == "retry" && r.Method == http.MethodPost {
		var kind string
		err := m.db.QueryRowContext(r.Context(), `SELECT kind FROM jobs WHERE id=?`, id).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "job_not_found", Message: "No se encontró el trabajo."})
			return true
		}
		if err != nil {
			writeAPIError(w, internalError(err))
			return true
		}
		if kind != "derive" && kind != "case" && kind != "evaluate" {
			return false
		}
		if kind == "derive" || kind == "evaluate" {
			legacyInferenceGone(w)
			return true
		}
		jobID, apiErr := m.retryLearningJob(r.Context(), id)
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return true
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID, "status": "queued"})
		return true
	}
	return false
}

func (m *Module) latestLearningOrigin(ctx context.Context, taskID string) (any, error) {
	var caseID, revision, projectHash, projectKey, promptHash, providerHash, provenance, note string
	var captured int
	var confirmed float64
	err := m.db.QueryRowContext(ctx, `SELECT case_id,source_revision,project_hash,project_key,prompt_hash,provider_config_hash,captured_before_task,provenance,note,confirmed_at FROM task_origins WHERE task_id=? ORDER BY id DESC LIMIT 1`, taskID).Scan(&caseID, &revision, &projectHash, &projectKey, &promptHash, &providerHash, &captured, &provenance, &note, &confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"case_id": caseID, "source_revision": revision, "project_hash": projectHash, "project_key": projectKey, "prompt_hash": promptHash, "provider_config_hash": providerHash, "captured_before_task": captured != 0, "provenance": provenance, "promotion_eligible": captured != 0 && (provenance == "system_recorded" || provenance == "human_attested"), "note": note, "confirmed_at": confirmed}, nil
}

var _ = strings.TrimSpace

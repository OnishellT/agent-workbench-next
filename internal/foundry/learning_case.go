// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"archive/tar"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxLearningSnapshotBytes  = int64(100_000_000)
	maxLearningSnapshotFiles  = 10_000
	maxLearningVerifierOutput = 500_000
)

type learningCaseRequest struct {
	Title            string   `json:"title"`
	Prompt           string   `json:"prompt"`
	Path             string   `json:"path"`
	ProjectPath      string   `json:"project_path"`
	VerifierPath     string   `json:"verifier_path"`
	Ref              string   `json:"ref"`
	GitRef           string   `json:"git_ref"`
	Domain           string   `json:"domain"`
	Split            string   `json:"split"`
	Verify           []string `json:"verify"`
	PositiveControl  []string `json:"positive_control"`
	NegativeControl  []string `json:"negative_control"`
	Negative         []string `json:"negative"`
	EpisodeID        string   `json:"episode_id"`
	TaskID           string   `json:"task_id"`
	ExpectedRevision string   `json:"expected_revision"`
	Exclude          []string `json:"exclude"`
}

type learningSnapshotCoverage struct {
	Files   int      `json:"files"`
	Bytes   int64    `json:"bytes"`
	Omitted []string `json:"omitted"`
}

type learningVerifierReceipt struct {
	Phase      string  `json:"phase"`
	Status     string  `json:"status"`
	ReturnCode *int    `json:"returncode"`
	Seconds    float64 `json:"seconds"`
	Stdout     string  `json:"stdout,omitempty"`
	Stderr     string  `json:"stderr,omitempty"`
	Error      string  `json:"error,omitempty"`
}

func (m *Module) queueLearningCase(w http.ResponseWriter, r *http.Request) {
	var request learningCaseRequest
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	request.Title = strings.TrimSpace(request.Title)
	request.Prompt = strings.TrimSpace(request.Prompt)
	request.Domain = strings.TrimSpace(request.Domain)
	if request.ProjectPath != "" {
		if request.Path != "" && request.Path != request.ProjectPath {
			writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_project_path", Message: "Envía un único path de proyecto."})
			return
		}
		request.Path = request.ProjectPath
	}
	if request.Path == "" {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "project_required", Message: "Indica un project_path existente."})
		return
	}
	if request.GitRef != "" {
		if request.Ref != "" && request.Ref != request.GitRef {
			writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_git_ref", Message: "Envía un único ref de Git."})
			return
		}
		request.Ref = request.GitRef
	}
	if request.Split == "" {
		request.Split = "origin"
	}
	if request.NegativeControl == nil {
		request.NegativeControl = request.Negative
	}
	request.EpisodeID = strings.TrimSpace(request.EpisodeID)
	request.TaskID = strings.TrimSpace(request.TaskID)
	if request.TaskID != "" {
		if request.EpisodeID != "" && request.EpisodeID != request.TaskID {
			writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_task", Message: "episode_id y task_id deben coincidir."})
			return
		}
		request.EpisodeID = request.TaskID
	}
	if request.Title == "" || len(request.Title) > maxTaskTitle || request.Prompt == "" || len(request.Prompt) > maxHTTPBody || request.Domain == "" || len(request.Domain) > 200 {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_case", Message: "title, prompt y domain son obligatorios y deben estar acotados."})
		return
	}
	if request.Split != "origin" && request.Split != "holdout" && request.Split != "regression" {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_split", Message: "split debe ser origin, holdout o regression."})
		return
	}
	if apiErr := validateLearningArgv(request.Verify, "verify", true); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if apiErr := validateLearningArgv(request.NegativeControl, "negative_control", true); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if apiErr := validateLearningArgv(request.PositiveControl, "positive_control", false); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	for _, path := range request.Exclude {
		if !safeLearningPath(path) {
			writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_exclude", Message: "exclude sólo admite rutas relativas seguras."})
			return
		}
	}
	project, apiErr := resolveLearningDirectory(request.Path, "project_path")
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	request.Path = project
	if request.VerifierPath != "" {
		verifier, err := resolveLearningDirectory(request.VerifierPath, "verifier_path")
		if err != nil {
			writeAPIError(w, err)
			return
		}
		request.VerifierPath = verifier
		if sameOrContains(project, verifier) || sameOrContains(verifier, project) {
			writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_verifier_path", Message: "verifier_path debe ser un directorio separado del proyecto."})
			return
		}
	}
	foundryRoot := filepath.Join(m.stateDir, "foundry")
	if sameOrContains(foundryRoot, project) || sameOrContains(project, foundryRoot) || request.VerifierPath != "" && (sameOrContains(foundryRoot, request.VerifierPath) || sameOrContains(request.VerifierPath, foundryRoot)) {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "project_in_store", Message: "El proyecto y el almacén privado de Foundry deben estar separados."})
		return
	}
	if request.Ref != "" && (len(request.Ref) > 512 || strings.ContainsAny(request.Ref, "\x00\r\n")) {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_git_ref", Message: "ref de Git inválido."})
		return
	}
	if request.EpisodeID != "" {
		task, err := loadLearningTask(r.Context(), m.db, request.EpisodeID)
		if errors.Is(err, sql.ErrNoRows) {
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "task_not_found", Message: "No se encontró la tarea para vincular el caso."})
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
		if request.ExpectedRevision != "" && request.ExpectedRevision != task.Revision {
			writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "task_revision_changed", Message: "La revisión visible cambió; abre la tarea antes de crear su caso."})
			return
		}
		request.ExpectedRevision = task.Revision
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		writeAPIError(w, &APIError{Status: http.StatusServiceUnavailable, Code: "closed", Message: "Foundry se está cerrando."})
		return
	}
	jobID, err := newID("job_")
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	requestID, err := newID("case_request_")
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	now := nowSeconds()
	progress := LearningProgress{Message: "En cola para congelar snapshot y verificar controles", Phase: "queued", Coverage: "pending"}
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO jobs(id,kind,subject,status,result_json,error,created,updated,progress_json) VALUES(?,'case',?,'queued',NULL,'',?,?,?)`, jobID, requestID, now, now, mustJSON(progress)); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO learning_case_requests(job_id,title,prompt,project_path,verifier_path,git_ref,domain,split,verify_json,positive_control_json,negative_control_json,exclude_json,task_id,expected_revision,created) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, jobID, request.Title, request.Prompt, request.Path, request.VerifierPath, request.Ref, request.Domain, request.Split, mustJSON(request.Verify), mustJSON(request.PositiveControl), mustJSON(request.NegativeControl), mustJSON(request.Exclude), request.EpisodeID, request.ExpectedRevision, now); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if err := appendAudit(r.Context(), tx, "learning_job", jobID, "learning_case_queued", map[string]any{"task_id": request.EpisodeID, "split": request.Split, "domain": request.Domain}); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	jobCtx, cancel := context.WithCancel(m.closeCtx)
	m.jobs[jobID] = cancel
	m.wg.Add(1)
	go m.runLearningCaseJob(jobCtx, jobID, cancel)
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID, "status": "queued"})
}

func validateLearningArgv(argv []string, name string, required bool) *APIError {
	if !required && argv == nil {
		return nil
	}
	if len(argv) == 0 || len(argv) > 64 {
		return &APIError{Status: http.StatusBadRequest, Code: "invalid_argv", Message: name + " debe ser una lista JSON de argumentos sin shell."}
	}
	for _, arg := range argv {
		if arg == "" || len(arg) > 4096 || strings.ContainsAny(arg, "\x00\r\n") {
			return &APIError{Status: http.StatusBadRequest, Code: "invalid_argv", Message: name + " contiene un argumento inválido."}
		}
	}
	return nil
}

func resolveLearningDirectory(raw, name string) (string, *APIError) {
	abs, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil {
		return "", &APIError{Status: http.StatusBadRequest, Code: "invalid_path", Message: name + " inválido."}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", &APIError{Status: http.StatusBadRequest, Code: "invalid_path", Message: name + " debe ser un directorio existente."}
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", &APIError{Status: http.StatusBadRequest, Code: "invalid_path", Message: name + " debe ser un directorio real."}
	}
	return resolved, nil
}

func sameOrContains(parent, child string) bool {
	parentAbs, e1 := filepath.Abs(parent)
	childAbs, e2 := filepath.Abs(child)
	if e1 != nil || e2 != nil {
		return true
	}
	rel, err := filepath.Rel(parentAbs, childAbs)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func (m *Module) runLearningCaseJob(ctx context.Context, jobID string, cancel context.CancelFunc) {
	defer m.wg.Done()
	defer cancel()
	defer m.forgetJob(jobID)
	res, err := m.db.ExecContext(context.Background(), `UPDATE jobs SET status='running',updated=?,progress_json=? WHERE id=? AND status='queued'`, nowSeconds(), mustJSON(LearningProgress{Message: "Preparando snapshot y controles", Phase: "snapshot", Coverage: "pending"}), jobID)
	if err != nil {
		return
	}
	count, _ := res.RowsAffected()
	if count != 1 {
		return
	}
	var req learningCaseRequest
	var taskID, revision, verify, positive, negative, exclude string
	err = m.db.QueryRowContext(ctx, `SELECT title,prompt,project_path,verifier_path,git_ref,domain,split,verify_json,positive_control_json,negative_control_json,exclude_json,task_id,expected_revision FROM learning_case_requests WHERE job_id=?`, jobID).Scan(&req.Title, &req.Prompt, &req.Path, &req.VerifierPath, &req.Ref, &req.Domain, &req.Split, &verify, &positive, &negative, &exclude, &taskID, &revision)
	if err == nil {
		err = json.Unmarshal([]byte(verify), &req.Verify)
	}
	if err == nil {
		err = json.Unmarshal([]byte(positive), &req.PositiveControl)
	}
	if err == nil {
		err = json.Unmarshal([]byte(negative), &req.NegativeControl)
	}
	if err == nil {
		err = json.Unmarshal([]byte(exclude), &req.Exclude)
	}
	var result map[string]any
	if err == nil {
		result, err = m.createLearningCase(ctx, jobID, req)
	}
	if err == nil && taskID != "" {
		linked, linkErr := m.linkLearningCaseToTask(ctx, jobID, taskID, revision, result["id"].(string))
		if linkErr != nil {
			result["link_warning"] = "Caso creado pero no vinculado; revisa la tarea y su revisión."
		} else if linked {
			result["linked_task_id"] = taskID
		} else {
			result["link_warning"] = "Caso creado pero no vinculado: la tarea/revisión cambió o el trabajo se canceló."
		}
	}
	status, message := "succeeded", ""
	if err != nil {
		status = "failed"
		message = safeLearningError(err.Error(), 2500)
		if ctx.Err() != nil {
			status, message = "cancelled", "cancelled"
		}
	}
	tx, txErr := m.db.BeginTx(context.Background(), nil)
	if txErr == nil {
		defer tx.Rollback()
		var current string
		if scanErr := tx.QueryRow(`SELECT status FROM jobs WHERE id=?`, jobID).Scan(&current); scanErr == nil && current == "running" {
			if ctx.Err() != nil {
				status, message = "cancelled", "cancelled"
			}
			var encoded any
			if status == "succeeded" {
				encoded = mustJSON(result)
			}
			_, txErr = tx.Exec(`UPDATE jobs SET status=?,result_json=?,error=?,updated=?,progress_json=? WHERE id=? AND status='running'`, status, encoded, message, nowSeconds(), mustJSON(LearningProgress{Message: map[bool]string{true: "Caso validado y guardado.", false: message}[status == "succeeded"], Phase: status, Coverage: map[bool]string{true: "complete", false: "partial"}[status == "succeeded"], Percent: map[bool]int{true: 100, false: 0}[status == "succeeded"]}), jobID)
			if txErr == nil {
				txErr = appendAudit(context.Background(), tx, "learning_job", jobID, "learning_case_finished", map[string]any{"status": status, "error": message})
			}
			if txErr == nil {
				txErr = tx.Commit()
			}
		}
	}
	_ = txErr
}

func (m *Module) linkLearningCaseToTask(ctx context.Context, jobID, taskID, expectedRevision, caseID string) (bool, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var status, revision string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=?`, jobID).Scan(&status); err != nil {
		return false, err
	}
	if status != "running" {
		return false, tx.Commit()
	}
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM tasks WHERE id=?`, taskID).Scan(&revision); err != nil {
		return false, err
	}
	if revision != expectedRevision {
		return false, tx.Commit()
	}
	var previous sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(generation) FROM task_case_selections WHERE task_id=?`, taskID).Scan(&previous); err != nil {
		return false, err
	}
	generation := int64(1)
	var ids []string
	if previous.Valid {
		generation = previous.Int64 + 1
		rows, err := tx.QueryContext(ctx, `SELECT case_id FROM task_case_selection_items WHERE task_id=? AND generation=? ORDER BY position`, taskID, previous.Int64)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return false, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, err
		}
		rows.Close()
	}
	for _, id := range ids {
		if id == caseID {
			return false, tx.Commit()
		}
	}
	ids = append(ids, caseID)
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_case_selections(task_id,generation,selection_hash,created) VALUES(?,?,?,?)`, taskID, generation, hashJSON(ids), nowSeconds()); err != nil {
		return false, err
	}
	for position, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_case_selection_items(task_id,generation,case_id,position) VALUES(?,?,?,?)`, taskID, generation, id, position); err != nil {
			return false, err
		}
	}
	if err := appendAudit(ctx, tx, "task", taskID, "case_linked", map[string]any{"case_id": caseID, "revision": revision, "generation": generation}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Module) createLearningCase(ctx context.Context, jobID string, request learningCaseRequest) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	project, err := resolveLearningPathForWorker(request.Path)
	if err != nil {
		return nil, err
	}
	foundryRoot := filepath.Join(m.stateDir, "foundry")
	if sameOrContains(foundryRoot, project) || sameOrContains(project, foundryRoot) {
		return nil, errors.New("El proyecto y el almacén de Foundry deben estar separados.")
	}
	caseID, err := newID("case_")
	if err != nil {
		return nil, err
	}
	snapshotsRoot := filepath.Join(foundryRoot, "snapshots")
	if err := os.MkdirAll(snapshotsRoot, 0700); err != nil {
		return nil, err
	}
	root := filepath.Join(snapshotsRoot, caseID)
	if err := os.Mkdir(root, 0700); err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(root)
		}
	}()
	source := filepath.Join(root, "project")
	verifier := filepath.Join(root, "verifier")
	if err := os.Mkdir(source, 0700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(verifier, 0700); err != nil {
		return nil, err
	}
	var coverage learningSnapshotCoverage
	var commit string
	if request.Ref != "" {
		commit, coverage, err = learningGitSnapshot(ctx, project, source, request.Ref, request.Exclude)
	} else {
		coverage, err = copyLearningTree(ctx, project, source, request.Exclude)
	}
	if err != nil {
		return nil, err
	}
	if request.VerifierPath != "" {
		external, err := resolveLearningPathForWorker(request.VerifierPath)
		if err != nil {
			return nil, err
		}
		if sameOrContains(project, external) || sameOrContains(external, project) || sameOrContains(foundryRoot, external) || sameOrContains(external, foundryRoot) {
			return nil, errors.New("verifier_path debe ser un directorio separado del proyecto y del almacén")
		}
		if _, err := copyLearningTree(ctx, external, verifier, nil); err != nil {
			return nil, err
		}
	}
	verify, err := freezeLearningArgv(request.Verify, project, request.VerifierPath, verifier)
	if err != nil {
		return nil, err
	}
	positive := freezeLearningControl(request.PositiveControl, project)
	negative := freezeLearningControl(request.NegativeControl, project)
	verifierHash, err := learningTreeHash(verifier)
	if err != nil {
		return nil, err
	}
	controls := make([]learningVerifierReceipt, 0, 4)
	workspace, err := os.MkdirTemp(root, "controls-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workspace)
	workspacePath := filepath.Join(workspace, "workspace")
	if err := copyLearningTreeInto(ctx, source, workspacePath, nil); err != nil {
		return nil, err
	}
	if len(positive) > 0 {
		receipt := runLearningVerifier(ctx, positive, workspacePath, verifier)
		receipt.Phase = "positive_setup"
		controls = append(controls, receipt)
		if receipt.Status != "pass" {
			return nil, fmt.Errorf("El control positivo no se pudo preparar: %s", mustJSON(receipt))
		}
	}
	good := runLearningVerifier(ctx, verify, workspacePath, verifier)
	good.Phase = "positive_verify"
	controls = append(controls, good)
	if good.Status != "pass" {
		return nil, fmt.Errorf("El verificador debe aprobar el control positivo. Configura positive_control: %s", mustJSON(good))
	}
	badSetup := runLearningVerifier(ctx, negative, workspacePath, verifier)
	badSetup.Phase = "negative_setup"
	controls = append(controls, badSetup)
	if badSetup.Status != "pass" {
		return nil, fmt.Errorf("El control negativo debe preparar el defecto y terminar con código 0: %s", mustJSON(badSetup))
	}
	bad := runLearningVerifier(ctx, verify, workspacePath, verifier)
	bad.Phase = "negative_verify"
	controls = append(controls, bad)
	if bad.Status != "fail" {
		return nil, fmt.Errorf("El verificador no detectó el defecto del control negativo: %s", mustJSON(bad))
	}
	if hash, err := learningTreeHash(verifier); err != nil || hash != verifierHash {
		return nil, errors.New("Un control modificó el verificador congelado.")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	projectHash, err := learningTreeHash(source)
	if err != nil {
		return nil, err
	}
	snapshot := map[string]any{"schema": 1, "directory": "snapshots/" + caseID, "project_key": hashJSON(project), "project_hash": projectHash, "verifier_hash": verifierHash, "git_commit": nullableString(commit), "coverage": coverage, "controls": controls, "positive_control": positive, "verify": verify, "negative_control": negative, "created": nowSeconds()}
	snapshotJSON := mustJSON(snapshot)
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cases(id,title,prompt,snapshot_json,verify_json,positive_control_json,negative_control_json,domain,split,created) VALUES(?,?,?,?,?,?,?,?,?,?)`, caseID, request.Title, request.Prompt, snapshotJSON, mustJSON(verify), mustJSON(positive), mustJSON(negative), request.Domain, request.Split, nowSeconds()); err != nil {
		return nil, err
	}
	if err := appendAudit(ctx, tx, "case", caseID, "case_created", map[string]any{"domain": request.Domain, "split": request.Split, "project_hash": projectHash, "verifier_hash": verifierHash}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	keep = true
	return map[string]any{"id": caseID, "title": request.Title, "prompt": request.Prompt, "domain": request.Domain, "split": request.Split, "created": nowSeconds(), "snapshot": snapshot, "verify": verify, "positive_control": positive, "negative_control": negative, "verified": true, "verifier_status": good.Status, "negative_control_status": bad.Status}, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func resolveLearningPathForWorker(raw string) (string, error) {
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errors.New("El directorio indicado no existe.")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("El directorio indicado no es un directorio real.")
	}
	return resolved, nil
}

var learningExcludedNames = map[string]bool{".git": true, ".hg": true, ".svn": true, ".env": true, ".venv": true, "venv": true, "node_modules": true, "__pycache__": true, ".pytest_cache": true, ".mypy_cache": true, "dist": true, "build": true, "target": true, ".foundry-skills": true}
var learningSecretNames = map[string]bool{"credentials": true, "credentials.json": true, "secrets.json": true, "id_rsa": true, "id_ed25519": true}
var learningSecretSuffixes = map[string]bool{".pem": true, ".key": true, ".p12": true, ".pfx": true, ".sqlite": true, ".sqlite3": true}

func excludedLearningName(name string) bool {
	lower := strings.ToLower(name)
	return learningExcludedNames[strings.ToLower(name)] || strings.HasPrefix(strings.ToLower(name), ".env.") || learningSecretNames[lower] || learningSecretSuffixes[strings.ToLower(filepath.Ext(name))]
}
func excludedLearningPath(relative string, excludes []string) bool {
	for _, exclude := range excludes {
		if relative == exclude || strings.HasPrefix(relative, exclude+"/") {
			return true
		}
	}
	return false
}

func copyLearningTree(ctx context.Context, source, destination string, excludes []string) (learningSnapshotCoverage, error) {
	return copyLearningTreeIntoCount(ctx, source, destination, excludes, learningSnapshotCoverage{})
}
func copyLearningTreeInto(ctx context.Context, source, destination string, excludes []string) error {
	_, err := copyLearningTreeIntoCount(ctx, source, destination, excludes, learningSnapshotCoverage{})
	return err
}
func copyLearningTreeIntoCount(ctx context.Context, source, destination string, excludes []string, coverage learningSnapshotCoverage) (learningSnapshotCoverage, error) {
	if err := os.MkdirAll(destination, 0700); err != nil {
		return coverage, err
	}
	omitted := make([]string, 0)
	rootInfo, err := os.Lstat(source)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return coverage, errors.New("Snapshot source must be a real directory")
	}
	total := coverage.Bytes
	files := coverage.Files
	err = filepath.WalkDir(source, func(current string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		slash := filepath.ToSlash(rel)
		if excludedLearningPath(slash, excludes) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			omitted = append(omitted, slash)
			return nil
		}
		if excludedLearningName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			omitted = append(omitted, slash)
			return nil
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			omitted = append(omitted, slash)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(destination, rel), 0700)
		}
		if !info.Mode().IsRegular() {
			omitted = append(omitted, slash)
			return nil
		}
		files++
		total += info.Size()
		if files > maxLearningSnapshotFiles || total > maxLearningSnapshotBytes {
			return errors.New("Snapshot excede el límite de 100 MB o 10000 archivos.")
		}
		src, err := os.Open(current)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(destination, rel)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0700); err != nil {
			src.Close()
			return err
		}
		mode := os.FileMode(0600)
		if info.Mode().Perm()&0111 != 0 {
			mode = 0700
		}
		dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			src.Close()
			return err
		}
		written, copyErr := io.Copy(dst, io.LimitReader(src, info.Size()+1))
		srcErr := src.Close()
		dstErr := dst.Close()
		if copyErr != nil {
			return copyErr
		}
		if srcErr != nil {
			return srcErr
		}
		if dstErr != nil {
			return dstErr
		}
		if written != info.Size() {
			return errors.New("El archivo cambió mientras se congelaba el snapshot.")
		}
		return nil
	})
	if err != nil {
		return coverage, err
	}
	sort.Strings(omitted)
	coverage.Files = files
	coverage.Bytes = total
	coverage.Omitted = append(coverage.Omitted, omitted...)
	return coverage, nil
}

func learningGitSnapshot(ctx context.Context, project, destination, ref string, excludes []string) (string, learningSnapshotCoverage, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return "", learningSnapshotCoverage{}, errors.New("git no está disponible para capturar el ref solicitado")
	}
	env := learningCommandEnv(filepath.Join(destination, ".git-home"), filepath.Join(destination, ".git-tmp"))
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, git, args...)
		cmd.Dir = project
		cmd.Env = env
		var out boundedBuffer
		out.limit = 64 * 1024
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return nil, err
		}
		if out.overflow {
			return nil, errors.New("git excedió el límite de salida")
		}
		return out.data.Bytes(), nil
	}
	resolved, err := run("rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", learningSnapshotCoverage{}, errors.New("git_ref no es un commit válido")
	}
	commit := strings.TrimSpace(string(resolved))
	if commit == "" || len(commit) > 64 {
		return "", learningSnapshotCoverage{}, errors.New("git devolvió una identidad de commit inválida")
	}
	archiveFile, err := os.CreateTemp(filepath.Dir(destination), ".foundry-source-*.tar")
	if err != nil {
		return "", learningSnapshotCoverage{}, err
	}
	archivePath := archiveFile.Name()
	if err := archiveFile.Close(); err != nil {
		_ = os.Remove(archivePath)
		return "", learningSnapshotCoverage{}, err
	}
	defer os.Remove(archivePath)
	if _, err := run("archive", "--format=tar", "--output="+archivePath, commit); err != nil {
		return "", learningSnapshotCoverage{}, errors.New("no se pudo capturar el commit")
	}
	archiveFile, err = os.Open(archivePath)
	if err != nil {
		return "", learningSnapshotCoverage{}, err
	}
	defer archiveFile.Close()
	coverage, err := extractLearningTar(ctx, archiveFile, destination, excludes)
	return commit, coverage, err
}

func extractLearningTar(ctx context.Context, input io.Reader, destination string, excludes []string) (learningSnapshotCoverage, error) {
	reader := tar.NewReader(input)
	coverage := learningSnapshotCoverage{}
	omitted := make([]string, 0)
	for {
		if err := ctx.Err(); err != nil {
			return coverage, err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return coverage, err
		}
		name := filepath.ToSlash(filepath.Clean(header.Name))
		if name == "." || name == "" {
			continue
		}
		if filepath.IsAbs(header.Name) || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || !safeLearningPath(name) {
			omitted = append(omitted, name)
			continue
		}
		if excludedLearningPath(name, excludes) {
			omitted = append(omitted, name)
			continue
		}
		parts := strings.Split(name, "/")
		skip := false
		for _, part := range parts {
			if excludedLearningName(part) {
				skip = true
				break
			}
		}
		if skip {
			omitted = append(omitted, name)
			continue
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return coverage, err
			}
		case tar.TypeReg:
			if header.Size < 0 || coverage.Files+1 > maxLearningSnapshotFiles || coverage.Bytes+header.Size > maxLearningSnapshotBytes {
				return coverage, errors.New("Commit demasiado grande.")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return coverage, err
			}
			mode := os.FileMode(0600)
			if header.Mode&0111 != 0 {
				mode = 0700
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return coverage, err
			}
			written, copyErr := io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return coverage, copyErr
			}
			if closeErr != nil {
				return coverage, closeErr
			}
			if written != header.Size {
				return coverage, io.ErrUnexpectedEOF
			}
			coverage.Files++
			coverage.Bytes += written
		default:
			omitted = append(omitted, name)
		}
	}
	sort.Strings(omitted)
	coverage.Omitted = omitted
	return coverage, nil
}

func freezeLearningArgv(argv []string, project, externalVerifier, frozenVerifier string) ([]string, error) {
	out := make([]string, len(argv))
	for index, arg := range argv {
		expanded := strings.ReplaceAll(arg, "{workspace}", project)
		candidate := expanded
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(project, candidate)
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			out[index] = arg
			continue
		}
		copyRoot := ""
		if pathWithin(project, abs) {
			copyRoot = project
		} else if externalVerifier != "" {
			resolved, err := filepath.EvalSymlinks(externalVerifier)
			if err == nil && pathWithin(resolved, abs) {
				copyRoot = resolved
			}
		}
		if copyRoot == "" {
			out[index] = arg
			continue
		}
		rel, err := filepath.Rel(copyRoot, abs)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			out[index] = arg
			continue
		}
		excluded := false
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if excludedLearningName(part) {
				excluded = true
				break
			}
		}
		if excluded {
			return nil, errors.New("verifier argv referencia una ruta excluida por privacidad")
		}
		source := abs
		destination := filepath.Join(frozenVerifier, rel)
		info, err := os.Lstat(source)
		if err != nil {
			out[index] = arg
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("verifier argv contiene un enlace simbólico")
		}
		if err := copyLearningEntry(source, destination); err != nil {
			return nil, err
		}
		out[index] = "{verifier}/" + filepath.ToSlash(rel)
	}
	return out, nil
}
func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func copyLearningEntry(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("verifier argv contiene un enlace simbólico")
	}
	if info.IsDir() {
		return filepath.WalkDir(source, func(current string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(source, current)
			if err != nil {
				return err
			}
			target := destination
			if rel != "." {
				target = filepath.Join(destination, rel)
			}
			if d.IsDir() {
				return os.MkdirAll(target, 0700)
			}
			if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() {
				return errors.New("verifier argv contiene un archivo no regular")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			data, err := os.ReadFile(current)
			if err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if info.Mode().Perm()&0111 != 0 {
				mode = 0700
			}
			return os.WriteFile(target, data, mode)
		})
	}
	if !info.Mode().IsRegular() {
		return errors.New("verifier argv contiene un archivo no regular")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if info.Mode().Perm()&0111 != 0 {
		mode = 0700
	}
	return os.WriteFile(destination, data, mode)
}

func freezeLearningControl(argv []string, project string) []string {
	out := make([]string, len(argv))
	for i, arg := range argv {
		out[i] = strings.ReplaceAll(arg, project, "{workspace}")
	}
	return out
}
func expandLearningArgv(argv []string, workspace, verifier string) []string {
	out := make([]string, len(argv))
	for i, arg := range argv {
		out[i] = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(arg, "{workspace}", workspace), "{verifier}", verifier), "{python}", "python3")
	}
	return out
}
func learningCommandEnv(home, tmp string) []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	return []string{"PATH=" + path, "HOME=" + home, "TMPDIR=" + tmp, "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "PYTHONDONTWRITEBYTECODE=1", "GIT_CONFIG_GLOBAL=/dev/null", "NO_COLOR=1"}
}

func runLearningVerifier(parent context.Context, argv []string, workspace, verifier string) learningVerifierReceipt {
	started := time.Now()
	receipt := learningVerifierReceipt{Status: "infrastructure_error"}
	if len(argv) == 0 {
		receipt.Error = "comando de verificación vacío"
		return receipt
	}
	scratchRoot := filepath.Join(filepath.Dir(workspace), ".verifier-tmp")
	if err := os.MkdirAll(scratchRoot, 0700); err != nil {
		receipt.Error = err.Error()
		return receipt
	}
	scratch, err := os.MkdirTemp(scratchRoot, "run-")
	if err != nil {
		receipt.Error = err.Error()
		return receipt
	}
	defer os.RemoveAll(scratch)
	home := filepath.Join(scratch, "home")
	tmp := filepath.Join(scratch, "tmp")
	if err := os.MkdirAll(home, 0700); err != nil {
		receipt.Error = err.Error()
		return receipt
	}
	if err := os.MkdirAll(tmp, 0700); err != nil {
		receipt.Error = err.Error()
		return receipt
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	args := expandLearningArgv(argv, workspace, verifier)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = workspace
	cmd.Env = learningCommandEnv(home, tmp)
	var stdout, stderr boundedBuffer
	stdout.limit = maxLearningVerifierOutput
	stderr.limit = maxLearningVerifierOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	receipt.Seconds = time.Since(started).Seconds()
	receipt.Stdout = truncateRunes(stdout.data.String(), 8000)
	receipt.Stderr = truncateRunes(stderr.data.String(), 8000)
	if stdout.overflow || stderr.overflow {
		receipt.Error = "output verifier excede 500 KB"
		return receipt
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			receipt.ReturnCode = &code
			receipt.Status = "fail"
			if code == 126 || code == 127 {
				receipt.Status = "infrastructure_error"
				receipt.Error = err.Error()
			}
		} else {
			receipt.Error = err.Error()
			if ctx.Err() != nil {
				receipt.Error = ctx.Err().Error()
			}
		}
		return receipt
	}
	code := 0
	receipt.ReturnCode = &code
	receipt.Status = "pass"
	return receipt
}

func (m *Module) retryLearningCase(ctx context.Context, originalJobID string) (string, *APIError) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", &APIError{Status: http.StatusServiceUnavailable, Code: "closed", Message: "Foundry se está cerrando."}
	}
	var found int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM learning_case_requests WHERE job_id=?`, originalJobID).Scan(&found); err != nil {
		return "", internalError(err)
	}
	if found != 1 {
		return "", &APIError{Status: http.StatusConflict, Code: "job_not_retryable", Message: "No se encontró una solicitud inmutable de caso para reintentar."}
	}
	jobID, err := newID("job_")
	if err != nil {
		return "", internalError(err)
	}
	requestID, err := newID("case_request_")
	if err != nil {
		return "", internalError(err)
	}
	now := nowSeconds()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return "", internalError(err)
	}
	defer tx.Rollback()
	progress := LearningProgress{Message: "Reintento en cola para congelar snapshot y verificar controles", Phase: "queued", Coverage: "pending"}
	if _, err := tx.ExecContext(ctx, `INSERT INTO jobs(id,kind,subject,status,result_json,error,created,updated,progress_json) VALUES(?,'case',?,'queued',NULL,'',?,?,?)`, jobID, requestID, now, now, mustJSON(progress)); err != nil {
		return "", internalError(err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO learning_case_requests(job_id,title,prompt,project_path,verifier_path,git_ref,domain,split,verify_json,positive_control_json,negative_control_json,exclude_json,task_id,expected_revision,created) SELECT ?,title,prompt,project_path,verifier_path,git_ref,domain,split,verify_json,positive_control_json,negative_control_json,exclude_json,task_id,expected_revision,? FROM learning_case_requests WHERE job_id=?`, jobID, now, originalJobID)
	if err != nil {
		return "", internalError(err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return "", internalError(err)
		}
		return "", &APIError{Status: http.StatusConflict, Code: "job_not_retryable", Message: "La solicitud original de caso ya no está disponible."}
	}
	if err := appendAudit(ctx, tx, "learning_job", jobID, "learning_case_retry_queued", map[string]any{"retry_of": originalJobID}); err != nil {
		return "", internalError(err)
	}
	if err := tx.Commit(); err != nil {
		return "", internalError(err)
	}
	jobCtx, cancel := context.WithCancel(m.closeCtx)
	m.jobs[jobID] = cancel
	m.wg.Add(1)
	go m.runLearningCaseJob(jobCtx, jobID, cancel)
	return jobID, nil
}

func learningCaseControlsVerified(snapshot string) (bool, string, string) {
	var manifest map[string]any
	if err := json.Unmarshal([]byte(snapshot), &manifest); err != nil {
		return false, "", ""
	}
	controls, ok := manifest["controls"].([]any)
	if !ok {
		return false, "", ""
	}
	positive, negative := "", ""
	for _, raw := range controls {
		control, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		phase, _ := control["phase"].(string)
		status, _ := control["status"].(string)
		switch phase {
		case "positive_verify":
			positive = status
		case "negative_verify":
			negative = status
		}
	}
	return positive == "pass" && negative == "fail", positive, negative
}

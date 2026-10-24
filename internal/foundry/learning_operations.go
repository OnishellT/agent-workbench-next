// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type learningCaseRow struct {
	ID, Title, Prompt, Domain, Split     string
	Snapshot, Verify, Positive, Negative string
	Created                              float64
}

func (m *Module) selectedLearningCases(ctx context.Context, taskID string) (int64, []learningCaseRow, error) {
	var generation sql.NullInt64
	if err := m.db.QueryRowContext(ctx, `SELECT MAX(generation) FROM task_case_selections WHERE task_id=?`, taskID).Scan(&generation); err != nil {
		return 0, nil, err
	}
	if !generation.Valid {
		return 0, []learningCaseRow{}, nil
	}
	rows, err := m.db.QueryContext(ctx, `SELECT c.id,c.title,c.prompt,c.domain,c.split,c.snapshot_json,c.verify_json,c.positive_control_json,c.negative_control_json,c.created FROM task_case_selection_items i JOIN cases c ON c.id=i.case_id WHERE i.task_id=? AND i.generation=? ORDER BY i.position`, taskID, generation.Int64)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	out := make([]learningCaseRow, 0)
	for rows.Next() {
		var row learningCaseRow
		if err := rows.Scan(&row.ID, &row.Title, &row.Prompt, &row.Domain, &row.Split, &row.Snapshot, &row.Verify, &row.Positive, &row.Negative, &row.Created); err != nil {
			return 0, nil, err
		}
		out = append(out, row)
	}
	return generation.Int64, out, rows.Err()
}

func (m *Module) learningCasePaths(row learningCaseRow) (string, string, error) {
	var manifest map[string]any
	if err := json.Unmarshal([]byte(row.Snapshot), &manifest); err != nil {
		return "", "", errors.New("manifest de snapshot inválido")
	}
	directory, ok := manifest["directory"].(string)
	if !ok || !safeLearningPath(directory) || !strings.HasPrefix(directory, "snapshots/") {
		return "", "", errors.New("directorio de snapshot inválido")
	}
	root := filepath.Join(m.stateDir, "foundry")
	for _, protectedRoot := range []string{root, filepath.Join(root, "snapshots")} {
		info, err := os.Lstat(protectedRoot)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", "", errors.New("directorio de snapshots ausente o enlazado")
		}
	}
	folder := filepath.Join(root, filepath.FromSlash(directory))
	clean, err := filepath.Abs(folder)
	if err != nil {
		return "", "", err
	}
	base, err := filepath.Abs(filepath.Join(root, "snapshots"))
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(base, clean)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("snapshot fuera del almacén")
	}
	project, verifier := filepath.Join(clean, "project"), filepath.Join(clean, "verifier")
	for _, path := range []string{clean, project, verifier} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", "", errors.New("snapshot o verificador ausente o enlazado")
		}
	}
	projectHash, err := learningTreeHash(project)
	if err != nil {
		return "", "", err
	}
	verifierHash, err := learningTreeHash(verifier)
	if err != nil {
		return "", "", err
	}
	if manifest["project_hash"] != projectHash || manifest["verifier_hash"] != verifierHash {
		return "", "", errors.New("integridad del snapshot o verificador modificada")
	}
	return project, verifier, nil
}

func learningTreeHash(root string) (string, error) {
	entries := make([][]any, 0)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("snapshot contains a symbolic link")
		}
		if d.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("snapshot contains a non-regular file")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, []any{filepath.ToSlash(rel), hex.EncodeToString(h.Sum(nil)), info.Mode().Perm()&0111 != 0})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i][0].(string) < entries[j][0].(string) })
	return hashJSON(entries), nil
}

func (m *Module) learningReadiness(ctx context.Context, candidateID string, deep bool) (map[string]any, *APIError) {
	var taskID, raw, status string
	if err := m.db.QueryRowContext(ctx, `SELECT task_id,spec_json,status FROM candidates WHERE id=?`, candidateID).Scan(&taskID, &raw, &status); errors.Is(err, sql.ErrNoRows) {
		return nil, &APIError{Status: http.StatusNotFound, Code: "candidate_not_found", Message: "No se encontró el aprendizaje."}
	} else if err != nil {
		return nil, internalError(err)
	}
	task, err := loadLearningTask(ctx, m.db, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &APIError{Status: http.StatusConflict, Code: "task_missing", Message: "La tarea fuente del aprendizaje ya no existe."}
	} else if err != nil {
		return nil, internalError(err)
	}
	if task.HostID != "" {
		if _, ok := m.hostName(ctx, task.HostID); !ok {
			return nil, &APIError{Status: http.StatusNotFound, Code: "candidate_not_found", Message: "No se encontró el aprendizaje en el ámbito de hosts actual."}
		}
	}
	var spec map[string]any
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return nil, internalError(err)
	}
	blockers := make([]map[string]any, 0)
	warnings := make([]map[string]any, 0)
	add := func(target *[]map[string]any, code, message, action string) {
		*target = append(*target, map[string]any{"code": code, "message": message, "action": action})
	}
	if !task.Completed {
		add(&blockers, "task_incomplete", "Confirma la finalización de la tarea antes de evaluar.", "complete_task")
	}
	if task.Label != "correct" && task.Label != "incorrect" {
		add(&blockers, "task_unlabeled", "Marca la tarea como correcta o incorrecta.", "label_task")
	}
	if task.Coverage.Status != "complete" {
		add(&blockers, "source_incomplete", "La captura fuente está incompleta o su cobertura no está acreditada.", "derive")
	}
	stale, staleReason := learningCandidateStale(spec, status, task)
	if stale {
		add(&blockers, "candidate_stale", staleReason, "derive")
	}
	generation, cases, err := m.selectedLearningCases(ctx, taskID)
	if err != nil {
		return nil, internalError(err)
	}
	selected := make([]map[string]any, 0, len(cases))
	caseIDs := make([]string, 0, len(cases))
	for _, c := range cases {
		caseIDs = append(caseIDs, c.ID)
		selected = append(selected, map[string]any{"id": c.ID, "title": c.Title, "domain": c.Domain, "split": c.Split})
		var manifest map[string]any
		if json.Unmarshal([]byte(c.Snapshot), &manifest) != nil || manifest["project_key"] == nil || manifest["project_hash"] == nil || manifest["verifier_hash"] == nil || manifest["directory"] == nil {
			add(&blockers, "snapshot_invalid", "Recrea el snapshot/verificador de "+c.Title+": manifest incompleto.", "repair_snapshot")
			continue
		}
		var verify, negative []string
		if json.Unmarshal([]byte(c.Verify), &verify) != nil || len(verify) == 0 || json.Unmarshal([]byte(c.Negative), &negative) != nil || len(negative) == 0 {
			add(&blockers, "snapshot_invalid", "Recrea el snapshot/verificador de "+c.Title+": comandos de control inválidos.", "repair_snapshot")
			continue
		}
		if verified, positiveStatus, negativeStatus := learningCaseControlsVerified(c.Snapshot); !verified {
			add(&blockers, "controls_unverified", fmt.Sprintf("El caso %s requiere recibos de control positivo=pass y negativo=fail (observado: %s/%s).", c.Title, positiveStatus, negativeStatus), "repair_snapshot")
			continue
		}
		if deep {
			if _, _, err := m.learningCasePaths(c); err != nil {
				add(&blockers, "snapshot_invalid", "Recrea el snapshot/verificador de "+c.Title+": "+err.Error(), "repair_snapshot")
				continue
			}
		}
	}
	if !hasSplit(cases, "origin") {
		add(&blockers, "missing_origin", "Crea o selecciona un caso de origen con snapshot inicial y controles observables.", "create_origin")
	}
	if !hasSplit(cases, "holdout") {
		add(&blockers, "missing_holdout", "Añade un caso reservado distinto que compruebe transferencia.", "create_holdout")
	}
	if len(cases) > 0 {
		originKeys := map[string]bool{}
		for _, originCase := range cases {
			if originCase.Split != "origin" {
				continue
			}
			var originManifest map[string]any
			if json.Unmarshal([]byte(originCase.Snapshot), &originManifest) == nil {
				if key, ok := originManifest["project_key"].(string); ok && key != "" {
					originKeys[key] = true
				}
			}
		}
		matchesOrigin := false
		matchesHoldout := false
		for _, c := range cases {
			var manifest map[string]any
			_ = json.Unmarshal([]byte(c.Snapshot), &manifest)
			key, _ := manifest["project_key"].(string)
			if c.Split == "origin" && key != "" {
				originKeys[key] = true
			}
			applicable := learningCaseApplicable(spec, c, originKeys, key)
			if c.Split == "origin" && applicable {
				matchesOrigin = true
			}
			if c.Split == "holdout" && applicable {
				matchesHoldout = true
			}
		}
		if !matchesOrigin {
			add(&blockers, "scope_mismatch", "El dominio o alcance no coincide con ningún caso de origen.", "edit_scope")
		}
		if !matchesHoldout {
			add(&blockers, "holdout_scope_mismatch", "La capacidad no se aplicaría a ningún caso reservado.", "select_cases")
		}
		if spec["scope"] == "general" {
			domains := map[string]bool{}
			for _, c := range cases {
				domains[c.Domain] = true
			}
			if len(domains) < 2 {
				add(&blockers, "missing_domains", "Una capacidad general requiere casos de al menos dos dominios.", "select_cases")
			}
		}
		seen := map[string]bool{}
		for _, c := range cases {
			var manifest map[string]any
			_ = json.Unmarshal([]byte(c.Snapshot), &manifest)
			signature := hashJSON(map[string]any{"prompt": c.Prompt, "project": manifest["project_hash"], "verifier": manifest["verifier_hash"], "verify": c.Verify})
			if seen[signature] {
				add(&blockers, "duplicate_cases", "Hay casos idénticos; el escenario reservado necesita una tarea o verificación diferente.", "select_cases")
				break
			}
			seen[signature] = true
		}
	}
	cfg, err := m.loadTaskConfig(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	diagnostics := m.providerDiagnostics(cfg)
	replay := diagnostics.Replay
	if !replay.Available {
		add(&blockers, "provider_unavailable", "No se encuentra el ejecutable del proveedor de evaluación.", "settings")
	}
	repetitions := cfg.Repetitions
	pairs := len(cases) * repetitions
	variants := 2
	if task.Label == "incorrect" {
		variants = 1
	}
	first := pairs * 2
	allCalls := first * variants
	if first > cfg.MaxCalls {
		add(&blockers, "insufficient_budget", fmt.Sprintf("La primera comparación requiere %d llamadas; el presupuesto permite %d.", first, cfg.MaxCalls), "settings")
	} else if allCalls > cfg.MaxCalls {
		add(&warnings, "fallback_budget", fmt.Sprintf("Probar ambas formulaciones puede necesitar %d llamadas; el presupuesto actual sólo cubre parte del fallback.", allCalls), "settings")
	}
	if pairs < 6 {
		add(&warnings, "insufficient_pairs", "Con este número de pares no puede alcanzarse p ≤ 0.025 ni siquiera ganando todos.", "settings")
	}
	if cfg.Replay.Kind == "command" && !cfg.Replay.RealInference {
		add(&warnings, "mechanical_driver", "El driver está configurado como mecánico; no acredita mejora de un modelo.", "settings")
	}
	origin, err := m.latestLearningOrigin(ctx, taskID)
	if err != nil {
		return nil, internalError(err)
	}
	if origin == nil {
		add(&warnings, "origin_unverified", "Puedes medir reproducibilidad, pero para adoptar necesitas vincular un snapshot inicial con una atestación humana.", "configure_origin")
	}
	availableRows, err := m.db.QueryContext(ctx, `SELECT id,title,domain,split FROM cases ORDER BY created DESC LIMIT 500`)
	if err != nil {
		return nil, internalError(err)
	}
	available := make([]map[string]any, 0)
	for availableRows.Next() {
		var id, title, domain, split string
		if err := availableRows.Scan(&id, &title, &domain, &split); err != nil {
			availableRows.Close()
			return nil, internalError(err)
		}
		if domain != spec["domain"] && spec["scope"] != "general" {
			continue
		}
		available = append(available, map[string]any{"id": id, "title": title, "domain": domain, "split": split, "compatibility": "domain_match_requires_review"})
	}
	if err := availableRows.Err(); err != nil {
		availableRows.Close()
		return nil, internalError(err)
	}
	_ = availableRows.Close()
	return map[string]any{"candidate_id": candidateID, "episode_id": taskID, "ready": len(blockers) == 0, "blockers": blockers, "warnings": warnings, "selected_cases": selected, "available_cases": available, "case_ids": caseIDs, "selection_generation": generation, "budget": map[string]any{"cases": len(cases), "repetitions": repetitions, "calls_first_variant": first, "calls_all_variants": allCalls, "max_calls": cfg.MaxCalls}, "promotion": map[string]any{"origin_linked": origin != nil, "origin": origin}, "setup": map[string]any{"domain": spec["domain"], "scope": spec["scope"], "required_splits": []string{"origin", "holdout"}, "required_case_fields": []string{"project_path", "title", "prompt", "domain", "split", "verify", "negative_control"}, "positive_control": "Necesario si el snapshot inicial no satisface el verificador.", "integrity_checked": deep}, "provider": replay, "integrity_checked": deep}, nil
}

func hasSplit(cases []learningCaseRow, split string) bool {
	for _, c := range cases {
		if c.Split == split {
			return true
		}
	}
	return false
}
func learningCaseApplicable(spec map[string]any, c learningCaseRow, originKeys map[string]bool, key string) bool {
	if spec["scope"] == "general" {
		return true
	}
	if spec["domain"] != c.Domain {
		return false
	}
	if spec["scope"] != "project" {
		return true
	}
	return key != "" && originKeys[key]
}

func (m *Module) candidateReadinessRoute(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	deep := r.URL.Query().Get("deep") == "1"
	if v := r.URL.Query().Get("deep"); v != "" && v != "0" && v != "1" {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_query", Message: "deep debe ser 0 o 1."})
		return
	}
	result, apiErr := m.learningReadiness(r.Context(), id, deep)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (m *Module) setCandidateScope(w http.ResponseWriter, r *http.Request, id string) {
	var request struct {
		Scope  string `json:"scope"`
		Domain string `json:"domain"`
	}
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	request.Scope = strings.TrimSpace(request.Scope)
	request.Domain = strings.TrimSpace(request.Domain)
	if request.Scope != "project" && request.Scope != "domain" && request.Scope != "general" || request.Domain == "" || len(request.Domain) > 200 {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_scope", Message: "Indica un alcance y dominio válidos."})
		return
	}
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	defer tx.Rollback()
	var raw, status, taskID string
	if err := tx.QueryRowContext(r.Context(), `SELECT spec_json,status,task_id FROM candidates WHERE id=?`, id).Scan(&raw, &status, &taskID); errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "candidate_not_found", Message: "No se encontró el aprendizaje."})
		return
	} else if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if status == "accepted" || status == "evaluating" {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "candidate_active", Message: "No se puede cambiar el alcance de un candidato aceptado o en evaluación."})
		return
	}
	var spec map[string]any
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	before := hashShort([]byte(raw), 64)
	spec["scope"], spec["domain"] = request.Scope, request.Domain
	spec["fingerprint"] = hashJSON(map[string]any{"kind": spec["kind"], "scope": request.Scope, "domain": request.Domain, "positive": spec["positive"], "preventive": spec["preventive"]})
	after := mustJSON(spec)
	if _, err := tx.ExecContext(r.Context(), `UPDATE candidates SET spec_json=?,status='proposed' WHERE id=?`, after, id); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO candidate_history(candidate_id,action,before_hash,after_hash,detail_json,created) VALUES(?,?,?,?,?,?)`, id, "scope_changed", before, hashJSON(spec), mustJSON(map[string]any{"scope": request.Scope, "domain": request.Domain}), nowSeconds()); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE experiments SET status='stale',updated=? WHERE candidate_id=? AND status IN ('running','passed','inconclusive')`, nowSeconds(), id); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if err := appendAudit(r.Context(), tx, "candidate", id, "candidate_scope_changed", map[string]any{"scope": request.Scope, "domain": request.Domain}); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE jobs SET status='cancelled',error='candidate_scope_changed',updated=? WHERE kind='evaluate' AND subject=? AND status IN ('queued','running')`, nowSeconds(), id); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	m.cancelLearningWorkersForCandidate(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// validatedCaseIDs limpia la lista del contrato: acotada, sin vacíos y sin repetidos.
func validatedCaseIDs(ids []string) ([]string, *APIError) {
	if ids == nil || len(ids) > 500 {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "invalid_cases", Message: "case_ids debe ser una lista acotada de identificadores."}
	}
	unique := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, caseID := range ids {
		if caseID == "" || len(caseID) > 256 {
			return nil, &APIError{Status: http.StatusBadRequest, Code: "invalid_cases", Message: "Identificador de caso inválido."}
		}
		if !seen[caseID] {
			seen[caseID] = true
			unique = append(unique, caseID)
		}
	}
	return unique, nil
}

// Generación nueva de una selección de casos: el binding que ya usaba el candidato.
func writeTaskCaseSelection(ctx context.Context, tx *sql.Tx, taskID string, unique []string) (int64, *APIError) {
	for _, caseID := range unique {
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cases WHERE id=?`, caseID).Scan(&found); err != nil {
			return 0, internalError(err)
		}
		if found == 0 {
			return 0, &APIError{Status: http.StatusBadRequest, Code: "case_not_found", Message: "Caso desconocido: " + caseID}
		}
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0)+1 FROM task_case_selections WHERE task_id=?`, taskID).Scan(&generation); err != nil {
		return 0, internalError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_case_selections(task_id,generation,selection_hash,created) VALUES(?,?,?,?)`, taskID, generation, hashJSON(unique), nowSeconds()); err != nil {
		return 0, internalError(err)
	}
	for position, caseID := range unique {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_case_selection_items(task_id,generation,case_id,position) VALUES(?,?,?,?)`, taskID, generation, caseID, position); err != nil {
			return 0, internalError(err)
		}
	}
	return generation, nil
}

// Guardar selección del editor de la tarea: elección humana, sin candidato ni inferencia.
func (m *Module) setTaskCases(w http.ResponseWriter, r *http.Request, id string) {
	var request struct {
		CaseIDs            []string `json:"case_ids"`
		ExpectedGeneration *int64   `json:"expected_generation"`
	}
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if request.ExpectedGeneration == nil {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "expected_generation debe acompañar a la selección que viste."})
		return
	}
	unique, apiErr := validatedCaseIDs(request.CaseIDs)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	task, err := loadLearningTask(r.Context(), m.db, id)
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
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	defer tx.Rollback()
	var current int64
	if err := tx.QueryRowContext(r.Context(), `SELECT COALESCE(MAX(generation),0) FROM task_case_selections WHERE task_id=?`, id).Scan(&current); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if current != *request.ExpectedGeneration {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "case_selection_changed", Message: "La selección de esta tarea cambió; vuelve a leerla antes de guardar."})
		return
	}
	generation, apiErr := writeTaskCaseSelection(r.Context(), tx, id, unique)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if err := appendAudit(r.Context(), tx, "task", id, "task_cases_selected", map[string]any{"generation": generation, "case_ids": unique}); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (m *Module) setCandidateCases(w http.ResponseWriter, r *http.Request, id string) {
	var request struct {
		CaseIDs []string `json:"case_ids"`
	}
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	unique, apiErr := validatedCaseIDs(request.CaseIDs)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	defer tx.Rollback()
	var taskID, status string
	if err := tx.QueryRowContext(r.Context(), `SELECT task_id,status FROM candidates WHERE id=?`, id).Scan(&taskID, &status); errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "candidate_not_found", Message: "No se encontró el aprendizaje."})
		return
	} else if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if status == "accepted" || status == "evaluating" {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "candidate_active", Message: "Cancela la evaluación antes de cambiar los casos."})
		return
	}
	var active int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM jobs WHERE kind='evaluate' AND subject=? AND status IN ('queued','running')`, id).Scan(&active); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if active > 0 {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "evaluation_running", Message: "Cancela la evaluación en curso antes de cambiar sus casos."})
		return
	}
	generation, apiErr := writeTaskCaseSelection(r.Context(), tx, taskID, unique)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if err := appendAudit(r.Context(), tx, "candidate", id, "candidate_cases_selected", map[string]any{"task_id": taskID, "generation": generation, "case_ids": unique}); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	readiness, apiErr := m.learningReadiness(r.Context(), id, false)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "readiness": readiness})
}

func (m *Module) cancelLearningWorkersForCandidate(candidateID string) {
	m.cancelJobs(jobIDs(context.Background(), m.db, `SELECT id FROM jobs WHERE kind='evaluate' AND subject=? AND status='cancelled'`, candidateID)...)
}

func (m *Module) confirmLearningOrigin(w http.ResponseWriter, r *http.Request, taskID string) {
	var request struct {
		CaseID    string `json:"case_id"`
		Confirmed bool   `json:"confirmed_initial_state"`
		Note      string `json:"note"`
	}
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	note := strings.TrimSpace(request.Note)
	if !request.Confirmed {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "origin_confirmation_required", Message: "Confirma expresamente que el caso conserva el estado inicial de esta tarea."})
		return
	}
	if note == "" || len(note) > maxFeedback {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "origin_note_required", Message: "Explica cómo identificaste el estado inicial (por ejemplo, commit o snapshot previo)."})
		return
	}
	task, err := loadLearningTask(r.Context(), m.db, taskID)
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
	generation, cases, err := m.selectedLearningCases(r.Context(), taskID)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	_ = generation
	var selected *learningCaseRow
	for i := range cases {
		if cases[i].ID == request.CaseID && cases[i].Split == "origin" {
			selected = &cases[i]
			break
		}
	}
	if selected == nil {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "origin_case_unselected", Message: "Selecciona un caso de origen vinculado a esta tarea."})
		return
	}
	if _, _, err := m.learningCasePaths(*selected); err != nil {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "origin_snapshot_invalid", Message: "El snapshot de origen no supera la verificación de integridad."})
		return
	}
	if verified, _, _ := learningCaseControlsVerified(selected.Snapshot); !verified {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "origin_controls_unverified", Message: "El caso de origen requiere recibos de control positivo y negativo verificados antes de atestarlo."})
		return
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(selected.Snapshot), &manifest); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	cfg, err := m.loadTaskConfig(r.Context())
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	projectHash, _ := manifest["project_hash"].(string)
	projectKey, _ := manifest["project_key"].(string)
	if projectHash == "" || projectKey == "" {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "origin_snapshot_invalid", Message: "El snapshot no declara identidad de proyecto verificable."})
		return
	}
	now := nowSeconds()
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO task_origins(task_id,case_id,source_revision,project_hash,project_key,prompt_hash,provider_config_hash,captured_before_task,provenance,note,confirmed_at) VALUES(?,?,?,?,?,?,?,1,'human_attested',?,?)`, taskID, request.CaseID, task.Revision, projectHash, projectKey, hashJSON(selected.Prompt), hashJSON(cfg.Replay), note, now); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if err := appendAudit(r.Context(), tx, "task", taskID, "task_origin_confirmed", map[string]any{"case_id": request.CaseID, "source_revision": task.Revision, "project_hash": projectHash, "provenance": "human_attested"}); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	origin, err := m.latestLearningOrigin(r.Context(), taskID)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, origin)
}

func (m *Module) candidatePackage(w http.ResponseWriter, r *http.Request, id string) {
	var taskID, status, raw string
	if err := m.db.QueryRowContext(r.Context(), `SELECT task_id,status,spec_json FROM candidates WHERE id=?`, id).Scan(&taskID, &status, &raw); errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "candidate_not_found", Message: "No se encontró el aprendizaje."})
		return
	} else if err != nil {
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
	buffer := new(bytes.Buffer)
	archive := zip.NewWriter(buffer)
	seen := map[string]bool{}
	write := func(name, content string) error {
		if !safeLearningPath(name) || seen[name] {
			return errors.New("ruta de paquete insegura o duplicada")
		}
		seen[name] = true
		entry, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = entry.Write([]byte(content))
		return err
	}
	manifest, err := json.Marshal(map[string]any{"candidate_id": id, "task_id": taskID, "status": status, "spec": spec})
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if err = write("manifest.json", string(manifest)); err != nil {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "candidate_package_invalid", Message: err.Error()})
		return
	}
	total := len(manifest)
	for _, variantName := range []string{"positive", "preventive"} {
		variant, ok := spec[variantName].(map[string]any)
		if !ok {
			continue
		}
		instructions, _ := variant["instructions"].(string)
		if err := write(variantName+"/instructions.md", instructions); err != nil {
			writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "candidate_package_invalid", Message: err.Error()})
			return
		}
		total += len(instructions)
		files, _ := variant["files"].([]any)
		if len(files) > 12 {
			writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "candidate_package_invalid", Message: "El paquete supera el límite de archivos."})
			return
		}
		for _, rawFile := range files {
			file, ok := rawFile.(map[string]any)
			if !ok {
				writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "candidate_package_invalid", Message: "Archivo de candidato inválido."})
				return
			}
			relative, ok := file["path"].(string)
			content, contentOK := file["content"].(string)
			if !ok || !contentOK || !safeLearningPath(relative) {
				writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "candidate_package_invalid", Message: "Ruta o contenido de archivo inválido."})
				return
			}
			if err := write(variantName+"/"+relative, content); err != nil {
				writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "candidate_package_invalid", Message: err.Error()})
				return
			}
			total += len(content)
			if total > 100000 {
				writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "candidate_package_too_large", Message: "El paquete supera 100 KB."})
				return
			}
		}
	}
	if err := archive.Close(); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"", id))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buffer.Bytes())
}

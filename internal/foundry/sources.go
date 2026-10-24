// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type sourceCreateRequest struct {
	Path    string  `json:"path"`
	Adapter Adapter `json:"adapter"`
}

type sourcePatchRequest struct {
	Enabled *bool    `json:"enabled,omitempty"`
	Path    *string  `json:"path,omitempty"`
	Adapter *Adapter `json:"adapter,omitempty"`
}

type discoverRequest struct {
	Roots []sourceCreateRequest `json:"roots"`
}

type sourceCandidate struct {
	Path      string  `json:"path"`
	Adapter   Adapter `json:"adapter"`
	FileCount int     `json:"file_count"`
}

type unsupportedPath struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

type sourceRecord struct {
	ID      string
	Path    string
	Adapter Adapter
	Enabled bool
	Created float64
}

func (m *Module) listSources(w http.ResponseWriter, r *http.Request) {
	rows, err := m.db.QueryContext(r.Context(), `SELECT id,path,adapter,enabled,created,last_capture,last_scan_at,last_scan_status,last_scan_files,last_scan_changed,last_scan_errors,last_scan_limited,coverage_json,last_error FROM sources ORDER BY created,id`)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	defer rows.Close()
	sources := make([]SourceDTO, 0)
	for rows.Next() {
		var source SourceDTO
		var adapter string
		var enabled, limited int
		var lastCapture, lastScanAt sql.NullFloat64
		var status, coverageJSON, lastError string
		var files, changed, scanErrors int
		if err := rows.Scan(&source.ID, &source.Path, &adapter, &enabled, &source.Created, &lastCapture, &lastScanAt, &status, &files, &changed, &scanErrors, &limited, &coverageJSON, &lastError); err != nil {
			writeAPIError(w, internalError(err))
			return
		}
		source.Adapter = Adapter(adapter)
		source.Enabled = enabled != 0
		source.LastCapture = nullFloat(lastCapture)
		source.LastScanAt = nullFloat(lastScanAt)
		source.Error = lastError
		source.LastScan = LastScan{Status: status, Files: files, Changed: changed, Errors: scanErrors, Limited: limited != 0}
		if source.LastScanAt != nil {
			source.LastScan.FinishedAt = *source.LastScanAt
		}
		if err := json.Unmarshal([]byte(coverageJSON), &source.Coverage); err != nil {
			source.Coverage = CoverageSummary{Status: "unknown"}
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
}

func (m *Module) createSource(w http.ResponseWriter, r *http.Request) {
	var request sourceCreateRequest
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	root, apiErr := validateSourceRoot(request.Path)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if !supportedAdapter(request.Adapter) {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "unsupported_adapter", Message: "El adaptador no está soportado."})
		return
	}
	id, err := newID("source_")
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	created := nowSeconds()
	coverage, _ := jsonText(CoverageSummary{Status: "unknown"})
	_, err = m.db.ExecContext(r.Context(), `INSERT INTO sources(id,path,adapter,enabled,created,coverage_json) VALUES(?,?,?,?,?,?)`, id, root, string(request.Adapter), 1, created, coverage)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "source_exists", Message: "La ruta ya está registrada."})
			return
		}
		writeAPIError(w, internalError(err))
		return
	}
	source, err := m.loadSource(r.Context(), id)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"source": source})
}

func (m *Module) discoverSources(w http.ResponseWriter, r *http.Request) {
	var request discoverRequest
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if len(request.Roots) > 50 {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "too_many_roots", Message: "Se permiten hasta 50 raíces explícitas por solicitud."})
		return
	}
	candidates := make([]sourceCandidate, 0, len(request.Roots))
	unsupported := make([]unsupportedPath, 0)
	issues := make([]map[string]any, 0)
	for _, root := range request.Roots {
		path, apiErr := validateSourceRoot(root.Path)
		if apiErr != nil {
			unsupported = append(unsupported, unsupportedPath{Path: root.Path, Kind: "path", Reason: apiErr.Code})
			continue
		}
		if !supportedAdapter(root.Adapter) {
			unsupported = append(unsupported, unsupportedPath{Path: path, Kind: "adapter", Reason: "unsupported_adapter"})
			continue
		}
		files, skipped, excluded, limited, err := enumerateFiles(r.Context(), path, maxScanFiles)
		if err != nil {
			unsupported = append(unsupported, unsupportedPath{Path: path, Kind: "path", Reason: "unreadable"})
			continue
		}
		// A mixed tree keeps its candidate: the boundary excludes a subtree, it does not fail
		// the root, and the exclusion is stated with the reason that belongs to it.
		sensitiveCount, opencodeCount := 0, 0
		for _, entry := range excluded {
			if opencodeHistoryPath(entry) {
				opencodeCount++
			} else {
				sensitiveCount++
			}
		}
		if sensitiveCount > 0 {
			issues = append(issues, map[string]any{"path": path, "reason": "sensitive_path_excluded", "count": sensitiveCount})
		}
		if opencodeCount > 0 {
			issues = append(issues, map[string]any{"path": path, "reason": "opencode_history_not_supported", "count": opencodeCount})
		}
		if len(skipped) > 0 {
			issues = append(issues, map[string]any{"path": path, "reason": "unsupported_entries", "count": len(skipped)})
		}
		if limited {
			issues = append(issues, map[string]any{"path": path, "reason": "file_limit", "limit": maxScanFiles})
		}
		if len(files) == 0 {
			unsupported = append(unsupported, unsupportedPath{Path: path, Kind: "empty", Reason: "no_supported_json_files"})
			continue
		}
		candidates = append(candidates, sourceCandidate{Path: path, Adapter: root.Adapter, FileCount: len(files)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": candidates, "unsupported": unsupported, "issues": issues})
}

func (m *Module) patchSource(w http.ResponseWriter, r *http.Request, rawID string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var request sourcePatchRequest
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	count := 0
	if request.Enabled != nil {
		count++
	}
	if request.Path != nil {
		count++
	}
	if request.Adapter != nil {
		count++
	}
	if count != 1 {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_patch", Message: "Actualiza exactamente uno de enabled, path o adapter."})
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		writeAPIError(w, &APIError{Status: http.StatusServiceUnavailable, Code: "closed", Message: "Foundry se está cerrando."})
		return
	}
	if request.Enabled == nil {
		var activeScans int
		if err := m.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM jobs WHERE kind='scan' AND status IN ('queued','running')`).Scan(&activeScans); err != nil {
			writeAPIError(w, internalError(err))
			return
		}
		if activeScans > 0 {
			writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "scan_in_progress", Message: "Cancela el escaneo en curso antes de cambiar la ruta o el adaptador."})
			return
		}
	}
	var oldPath, oldAdapter string
	var oldEnabled int
	if err := m.db.QueryRowContext(r.Context(), `SELECT path,adapter,enabled FROM sources WHERE id=?`, id).Scan(&oldPath, &oldAdapter, &oldEnabled); errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "source_not_found", Message: "No se encontró la fuente."})
		return
	} else if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if request.Enabled != nil {
		_, err := m.db.ExecContext(r.Context(), `UPDATE sources SET enabled=? WHERE id=?`, boolInt(*request.Enabled), id)
		if err != nil {
			writeAPIError(w, internalError(err))
			return
		}
	} else if request.Path != nil {
		path, apiErr := validateSourceRoot(*request.Path)
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		_, err := m.db.ExecContext(r.Context(), `UPDATE sources SET path=? WHERE id=?`, path, id)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "source_exists", Message: "La ruta ya está registrada."})
				return
			}
			writeAPIError(w, internalError(err))
			return
		}
	} else {
		if !supportedAdapter(*request.Adapter) {
			writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "unsupported_adapter", Message: "El adaptador no está soportado."})
			return
		}
		_, err := m.db.ExecContext(r.Context(), `UPDATE sources SET adapter=? WHERE id=?`, string(*request.Adapter), id)
		if err != nil {
			writeAPIError(w, internalError(err))
			return
		}
	}
	source, err := m.loadSource(r.Context(), id)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	revoked := oldEnabled != 0 && request.Enabled != nil && !*request.Enabled
	if request.Path != nil && source.Path != oldPath {
		revoked = true
	}
	if request.Adapter != nil && string(*request.Adapter) != oldAdapter {
		revoked = true
	}
	if request.Enabled != nil && !*request.Enabled {
		m.cancelJobsLocked(jobIDs(r.Context(), m.db, "SELECT id FROM jobs WHERE kind='scan' AND subject=? AND status IN ('queued','running')", id)...)
	}
	if revoked {
		m.cancelJobsLocked(jobIDs(r.Context(), m.db, `SELECT j.id FROM jobs j JOIN task_proposal_receipts p ON p.job_id=j.id JOIN sessions s ON s.id=p.session_id WHERE j.kind='propose_tasks' AND j.status IN ('queued','running') AND s.source_id=?`, id)...)
	}
	if revoked {
		reason := "source_configuration_changed"
		if request.Enabled != nil {
			reason = "source_disabled"
		}
		if request.Path != nil {
			reason = "source_path_changed"
		}
		if request.Adapter != nil {
			reason = "source_adapter_changed"
		}
		if err := m.invalidateLearningForSource(r.Context(), id, reason); err != nil {
			writeAPIError(w, internalError(err))
			return
		}
		m.cancelLearningWorkersForSourceLocked(id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": source, "revoked": revoked})
}

func (m *Module) queueSourceScan(w http.ResponseWriter, r *http.Request, rawID string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	m.queueScanFor(w, r, id)
}

func (m *Module) queueScan(w http.ResponseWriter, r *http.Request) {
	var request struct {
		SourceID string `json:"source_id,omitempty"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if apiErr := decodeBody(w, r, &request); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
	}
	m.queueScanFor(w, r, request.SourceID)
}

func (m *Module) queueScanFor(w http.ResponseWriter, r *http.Request, sourceID string) {
	if sourceID != "" {
		var enabled int
		err := m.db.QueryRowContext(r.Context(), `SELECT enabled FROM sources WHERE id=?`, sourceID).Scan(&enabled)
		if errors.Is(err, sql.ErrNoRows) {
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "source_not_found", Message: "No se encontró la fuente."})
			return
		}
		if err != nil {
			writeAPIError(w, internalError(err))
			return
		}
		if enabled == 0 {
			writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "source_revoked", Message: "La fuente está desactivada."})
			return
		}
	} else {
		var count int
		if err := m.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM sources WHERE enabled=1`).Scan(&count); err != nil {
			writeAPIError(w, internalError(err))
			return
		}
		if count == 0 {
			writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "no_enabled_sources", Message: "No hay fuentes habilitadas para escanear."})
			return
		}
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		writeAPIError(w, &APIError{Status: http.StatusServiceUnavailable, Code: "closed", Message: "Foundry se está cerrando."})
		return
	}
	var active int
	if err := m.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM jobs WHERE kind='scan' AND status IN ('queued','running')`).Scan(&active); err != nil {
		m.mu.Unlock()
		writeAPIError(w, internalError(err))
		return
	}
	if active > 0 {
		m.mu.Unlock()
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "scan_in_progress", Message: "Ya hay un escaneo en curso."})
		return
	}
	jobID, err := newID("scan_")
	if err != nil {
		m.mu.Unlock()
		writeAPIError(w, internalError(err))
		return
	}
	now := nowSeconds()
	if _, err := m.db.ExecContext(r.Context(), `INSERT INTO jobs(id,kind,subject,status,created,updated) VALUES(?,'scan',?,'queued',?,?)`, jobID, sourceID, now, now); err != nil {
		m.mu.Unlock()
		writeAPIError(w, internalError(err))
		return
	}
	ctx, cancel := context.WithCancel(m.closeCtx)
	m.jobs[jobID] = cancel
	// Admission and WaitGroup.Add share m.mu with Close, so Wait cannot race with a later Add.
	m.wg.Add(1)
	go m.runScanJob(ctx, jobID, sourceID, cancel)
	m.mu.Unlock()
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID, "status": "queued", "source_id": sourceID})
}

// listLocalScanSources lists the enabled sources a full scan walks. Sources imported
// from a host ("host-history-*") are fed by the host importer, never scanned locally.
func (m *Module) listLocalScanSources(ctx context.Context) ([]sourceRecord, error) {
	all, err := queryAll(ctx, m.db, func(rows *sql.Rows) (sourceRecord, error) {
		var item sourceRecord
		var adapter string
		var enabled int
		err := rows.Scan(&item.ID, &item.Path, &adapter, &enabled, &item.Created)
		item.Adapter, item.Enabled = Adapter(adapter), enabled != 0
		return item, err
	}, "SELECT id,path,adapter,enabled,created FROM sources WHERE enabled=1 ORDER BY created,id")
	if err != nil {
		return nil, err
	}
	sources := all[:0]
	for _, item := range all {
		if !strings.HasPrefix(item.ID, "host-history-") {
			sources = append(sources, item)
		}
	}
	return sources, nil
}

func (m *Module) runScanJob(ctx context.Context, jobID, requestedSource string, cancel context.CancelFunc) {
	defer m.wg.Done()
	defer cancel()
	defer m.forgetJob(jobID)
	result := ScanResult{MaxFiles: maxScanFiles}
	if ctx.Err() != nil {
		m.finishJob(jobID, "cancelled", &result, "cancelled")
		return
	}
	m.captureMu.Lock()
	defer m.captureMu.Unlock()
	if ctx.Err() != nil {
		m.finishJob(jobID, "cancelled", &result, "cancelled")
		return
	}
	if _, err := m.db.ExecContext(context.Background(), "UPDATE jobs SET status='running',updated=? WHERE id=? AND status='queued'", nowSeconds(), jobID); err != nil {
		m.finishJob(jobID, "failed", nil, "database_error")
		return
	}
	var sources []sourceRecord
	if requestedSource == "" {
		var err error
		sources, err = m.listLocalScanSources(ctx)
		if err != nil {
			if ctx.Err() != nil {
				m.finishJob(jobID, "cancelled", &result, "cancelled")
			} else {
				m.finishJob(jobID, "failed", &result, "database_error")
			}
			return
		}
	} else {
		item, err := m.loadSourceRecord(ctx, requestedSource)
		if err != nil {
			m.finishJob(jobID, "failed", &result, "source_not_found")
			return
		}
		if !item.Enabled {
			m.finishJob(jobID, "cancelled", &result, "source_revoked")
			return
		}
		sources = append(sources, item)
	}
	for _, source := range sources {
		if ctx.Err() != nil {
			break
		}
		if err := m.scanSource(ctx, jobID, source, &result); err != nil {
			if ctx.Err() != nil {
				break
			}
			if errors.Is(err, errSourceRevoked) {
				if requestedSource != "" {
					m.finishJob(jobID, "cancelled", &result, "source_revoked")
					return
				}
				continue
			}
			result.Errors++
			m.setSourceError(source.ID, err.Error())
			m.updateLastScan(source.ID, ScanResult{Errors: 1, MaxFiles: maxScanFiles}, "failed")
		}
	}
	if ctx.Err() != nil {
		m.finishJob(jobID, "cancelled", &result, "cancelled")
		return
	}
	status, message := "succeeded", ""
	if result.Errors > 0 {
		status, message = "failed", "scan_errors"
	}
	m.finishJob(jobID, status, &result, message)
}

func (m *Module) sourceEnabled(ctx context.Context, source sourceRecord) (bool, error) {
	var enabled int
	var currentPath, currentAdapter string
	err := m.db.QueryRowContext(ctx, "SELECT enabled,path,adapter FROM sources WHERE id=?", source.ID).Scan(&enabled, &currentPath, &currentAdapter)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled != 0 && currentPath == source.Path && currentAdapter == string(source.Adapter), nil
}

func (m *Module) scanSource(ctx context.Context, jobID string, source sourceRecord, result *ScanResult) error {
	allowed, err := m.sourceEnabled(ctx, source)
	if err != nil {
		return err
	}
	if !allowed {
		return errSourceRevoked
	}
	files, skipped, excluded, limited, err := enumerateFiles(ctx, source.Path, maxScanFiles-result.Files)
	if err != nil {
		return err
	}
	sourceResult := ScanResult{MaxFiles: maxScanFiles, Errors: len(skipped), Limited: limited}
	sourceCoverage := CoverageSummary{Status: "unknown"}
	for _, path := range excluded[:min(len(excluded), maxCaptureIssues)] {
		reason := "sensitive_path_excluded"
		if opencodeHistoryPath(path) {
			reason = "opencode_history_not_supported"
		}
		sourceCoverage.Issues = append(sourceCoverage.Issues, Issue{FileName: filepath.Base(path), Reason: reason, Severity: "semantic"})
		sourceCoverage.IssueCount++
		sourceCoverage.SemanticIssueCount++
	}
	captured := make(map[string]struct{})
	for _, path := range skipped[:min(len(skipped), maxCaptureIssues)] {
		sourceCoverage.Issues = append(sourceCoverage.Issues, Issue{FileName: filepath.Base(path), Reason: "source_file_skipped", Severity: "blocking"})
	}
	result.Limited = result.Limited || limited
	result.Errors += len(skipped)
	result.MaxFiles = maxScanFiles
	for _, path := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		allowed, err := m.sourceEnabled(ctx, source)
		if err != nil {
			return err
		}
		if !allowed {
			return errSourceRevoked
		}
		result.CurrentFile = path
		sourceResult.CurrentFile = path
		result.Files++
		sourceResult.Files++
		m.updateJobProgress(jobID, *result)
		sessionID, changed, err := m.scanFile(ctx, source, path)
		if err != nil {
			if errors.Is(err, errSourceRevoked) {
				return err
			}
			result.Errors++
			sourceResult.Errors++
			m.setSourceError(source.ID, err.Error())
			sourceCoverage.Issues = append(sourceCoverage.Issues, Issue{FileName: filepath.Base(path), Reason: err.Error(), Severity: "blocking"})
			continue
		}
		if _, seen := captured[sessionID]; sessionID != "" && !seen {
			captured[sessionID] = struct{}{}
			result.Sessions++
		}
		if changed {
			result.Changed++
			sourceResult.Changed++
		}
		fileCoverage, err := m.transcriptCoverage(ctx, source.ID, path)
		if err != nil {
			result.Errors++
			sourceResult.Errors++
			m.setSourceError(source.ID, err.Error())
			sourceCoverage.Issues = append(sourceCoverage.Issues, Issue{FileName: filepath.Base(path), Reason: err.Error(), Severity: "blocking"})
			continue
		}
		sourceCoverage = mergeCoverage(sourceCoverage, fileCoverage)
	}
	coverage := sourceCoverage
	coverage.IssueCount += sourceResult.Errors
	coverage.BlockingIssueCount += sourceResult.Errors
	coverage.Issues = coverage.Issues[:min(len(coverage.Issues), maxCaptureIssues)]
	if sourceResult.Errors > 0 || sourceResult.Limited {
		coverage.Status = "partial"
	}
	finished := nowSeconds()
	lastCapture := any(nil)
	if sourceResult.Changed > 0 {
		lastCapture = finished
	}
	_, err = m.db.ExecContext(context.Background(), "UPDATE sources SET last_capture=coalesce(?,last_capture),last_scan_at=?,last_scan_status=?,last_scan_files=?,last_scan_changed=?,last_scan_errors=?,last_scan_max_files=?,last_scan_current_file=?,last_scan_limited=?,coverage_json=?,last_error=CASE WHEN ?>0 THEN 'scan_errors' ELSE '' END WHERE id=?", lastCapture, finished, scanStatus(&sourceResult), sourceResult.Files, sourceResult.Changed, sourceResult.Errors, sourceResult.MaxFiles, sourceResult.CurrentFile, boolInt(sourceResult.Limited), mustJSON(coverage), sourceResult.Errors, source.ID)
	if err == nil {
		result.Coverage = mergeCoverage(result.Coverage, coverage)
	}
	return err
}

func (m *Module) transcriptCoverage(ctx context.Context, sourceID, path string) (CoverageSummary, error) {
	var encoded string
	err := m.db.QueryRowContext(ctx, `SELECT coverage_json FROM sessions WHERE source_id=? AND path=?`, sourceID, path).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return CoverageSummary{Status: "unknown"}, nil
	}
	if err != nil {
		return CoverageSummary{}, err
	}
	var coverage CoverageSummary
	if err := json.Unmarshal([]byte(encoded), &coverage); err != nil {
		return CoverageSummary{}, err
	}
	return coverage, nil
}

func mergeCoverage(total, next CoverageSummary) CoverageSummary {
	if next.Status == "partial" {
		total.Status = "partial"
	} else if next.Status == "complete" && (total.Status == "" || total.Status == "unknown") {
		total.Status = "complete"
	}
	total.EventCount += next.EventCount
	total.IssueCount += next.IssueCount
	total.BlockingIssueCount += next.BlockingIssueCount
	total.SemanticIssueCount += next.SemanticIssueCount
	total.Issues = append(total.Issues, next.Issues...)
	total.Issues = total.Issues[:min(len(total.Issues), maxCaptureIssues)]
	total.Bytes += next.Bytes
	if next.ActivityAt > total.ActivityAt {
		total.ActivityAt = next.ActivityAt
	}
	total.AppendPending = total.AppendPending || next.AppendPending
	if total.NormalizerVersion == "" {
		total.NormalizerVersion = next.NormalizerVersion
	}
	if total.RedactionVersion == "" {
		total.RedactionVersion = next.RedactionVersion
	}
	return total
}

// scanFile normalizes and persists one file and returns the conversation it left captured
// (empty when the file was refused, unreadable or produced no session).
func (m *Module) scanFile(ctx context.Context, source sourceRecord, path string) (string, bool, error) {
	allowed, err := m.sourceEnabled(ctx, source)
	if err != nil {
		return "", false, err
	}
	if !allowed {
		return "", false, errSourceRevoked
	}
	if err := validateNoSymlinkComponents(source.Path, path); err != nil {
		return "", false, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return "", false, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return "", false, errors.New("unsupported_file_type")
	}
	if before.Size() > MaxSourceBytes {
		return "", false, ErrSourceTooLarge
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return "", false, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		file.Close()
		return "", false, errors.New("source_changed_or_symlinked")
	}
	fallback := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	options := NormalizeOptions{Adapter: source.Adapter, FallbackSessionID: fallback, MaxBytes: MaxSourceBytes}
	var transcript Transcript
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".json" {
		transcript, err = NormalizeJSON(file, options)
	} else {
		transcript, err = NormalizeJSONL(file, options)
	}
	closeErr := file.Close()
	if err != nil {
		return "", false, err
	}
	if closeErr != nil {
		return "", false, closeErr
	}
	after, err := os.Lstat(path)
	if err != nil {
		return "", false, err
	}
	if err := validateNoSymlinkComponents(source.Path, path); err != nil {
		return "", false, errors.New("source_changed_or_symlinked")
	}
	if after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", false, errors.New("source_changed_during_scan")
	}
	if ctx.Err() != nil {
		return "", false, ctx.Err()
	}
	allowed, err = m.sourceEnabled(ctx, source)
	if err != nil {
		return "", false, err
	}
	if !allowed {
		return "", false, errSourceRevoked
	}
	candidate := transcriptSessionID(transcript.Events, fallback)
	id, err := m.sessionIDFor(source.ID, candidate)
	if err != nil {
		return "", false, err
	}
	hostID, err := transcriptHostID(transcript.Events)
	if err != nil {
		return "", false, err
	}
	if hostID == "" {
		hostID = "local"
	}
	if hostID != "" && hostID != "local" {
		if m.hostScope == nil {
			return "", false, errors.New("host_scope_unavailable")
		}
		if _, err := m.hostScope.Get(ctx, hostID); err != nil {
			return "", false, errors.New("host_scope_denied")
		}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", false, err
	}
	changed, err := m.persistTranscript(ctx, source, id, path, transcript, after.ModTime(), hostID)
	if err != nil {
		return "", false, err
	}
	return id, changed, nil
}

func (m *Module) persistTranscript(ctx context.Context, source sourceRecord, sessionID, path string, transcript Transcript, fileMtime time.Time, hostID string) (bool, error) {
	now := nowSeconds()
	activity := SessionActivity(transcript.Events, fileMtime)
	coverage := summaryFromCoverage(transcript.Coverage, activity, path)
	completion := "unknown"
	for _, event := range transcript.Events {
		if eventBool(event["completion_suggested"]) {
			completion = "suggested"
			break
		}
	}
	title := titleFromEvents(transcript.Events, sessionID)
	// The adapter is part of the identity of a capture: the same bytes read with another
	// adapter produce other events, so they must be another revision, not a cached one.
	revision := hashShort([]byte(transcript.Coverage.RawSHA256+"\x00"+string(source.Adapter)+"\x00"+NormalizerVersion+"\x00"+RedactionVersion), 64)
	coverageJSON, err := jsonText(coverage)
	if err != nil {
		return false, err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var enabled int
	var currentPath, currentAdapter string
	if err := tx.QueryRowContext(ctx, "SELECT enabled,path,adapter FROM sources WHERE id=?", source.ID).Scan(&enabled, &currentPath, &currentAdapter); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, errSourceRevoked
		}
		return false, err
	}
	if enabled == 0 || currentPath != source.Path || currentAdapter != string(source.Adapter) {
		return false, errSourceRevoked
	}
	var oldSource, oldRevision string
	lookupErr := tx.QueryRowContext(ctx, `SELECT source_id,revision FROM sessions WHERE id=?`, sessionID).Scan(&oldSource, &oldRevision)
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return false, lookupErr
	}
	if lookupErr == nil && oldSource != source.ID {
		return false, errors.New("session_id_collision")
	}
	if oldRevision == revision {
		_, err := tx.ExecContext(ctx, `UPDATE sessions SET path=?,updated=?,captured_at=?,activity_at=?,completion=?,coverage_json=?,host_id=? WHERE id=?`, path, now, now, activity, completion, coverageJSON, hostID, sessionID)
		if err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if errors.Is(lookupErr, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO sessions(id,source_id,path,title,revision,updated,captured_at,activity_at,completion,coverage_json,host_id) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, sessionID, source.ID, path, title, revision, now, now, activity, completion, coverageJSON, hostID)
	} else {
		// A->B->A: the revision row and its events already exist (revisions are keyed
		// by content), so only repoint the session at it instead of re-inserting.
		var known int
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions WHERE session_id=? AND revision=?`, sessionID, revision).Scan(&known)
		if err == nil && known == 0 {
			err = insertRevision(ctx, tx, sessionID, revision, transcript, now, coverageJSON)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE sessions SET path=?,title=?,revision=?,updated=?,captured_at=?,activity_at=?,completion=?,coverage_json=?,host_id=? WHERE id=?`, path, title, revision, now, now, activity, completion, coverageJSON, hostID, sessionID)
		}
	}
	if err != nil {
		return false, err
	}
	if errors.Is(lookupErr, sql.ErrNoRows) {
		if err := insertRevision(ctx, tx, sessionID, revision, transcript, now, coverageJSON); err != nil {
			return false, err
		}
	}
	if err := appendAudit(ctx, tx, "conversation", sessionID, "revision_captured", map[string]any{"source_id": source.ID, "revision": revision, "raw_sha256": transcript.Coverage.RawSHA256, "event_count": len(transcript.Events)}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// insertRevision stores a revision row and its events; the session row must already exist.
func insertRevision(ctx context.Context, tx *sql.Tx, sessionID, revision string, transcript Transcript, created float64, coverageJSON string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO revisions(session_id,revision,raw_sha256,created,coverage_json) VALUES(?,?,?,?,?)`, sessionID, revision, transcript.Coverage.RawSHA256, created, coverageJSON); err != nil {
		return err
	}
	return insertEvents(ctx, tx, sessionID, revision, transcript.Events)
}

func insertEvents(ctx context.Context, tx *sql.Tx, sessionID, revision string, events []Event) error {
	// One prepared statement for the whole revision: the driver would otherwise
	// re-parse the 17-column INSERT for every event.
	insert, err := tx.PrepareContext(ctx, `INSERT INTO events(session_id,revision,event_index,event_id,source_line,role,kind,text,timestamp_json,channel,tool_name,call_id,attachments_json,evidence_json,inert,completion_suggested,native_type) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	for index, event := range events {
		evidence, err := jsonText(event)
		if err != nil {
			return err
		}
		attachments := event["attachments"]
		if attachments == nil {
			attachments = []any{}
		}
		attachmentsJSON, err := jsonText(attachments)
		if err != nil {
			return err
		}
		timestampJSON, err := jsonText(event["timestamp"])
		if err != nil {
			return err
		}
		if timestampJSON == "null" {
			timestampJSON = ""
		}
		_, err = insert.ExecContext(ctx,
			sessionID, revision, index, stringField(event["id"]), intField(event["line"]), stringField(event["role"]), stringField(event["kind"]), stringField(event["text"]), timestampJSON, stringField(event["channel"]), stringField(event["tool_name"]), stringField(event["call_id"]), attachmentsJSON, evidence, boolInt(event["inert"] == true), boolInt(eventBool(event["completion_suggested"])), stringField(event["native_type"]))
		if err != nil {
			return err
		}
	}
	return nil
}

func enumerateFiles(ctx context.Context, root string, max int) ([]string, []string, []string, bool, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, nil, nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, nil, false, errors.New("source_root_symlink")
	}
	if info.Mode().IsRegular() {
		if supportedFile(root) {
			return []string{root}, nil, nil, false, nil
		}
		return nil, []string{root}, nil, false, nil
	}
	if !info.IsDir() {
		return nil, nil, nil, false, errors.New("unsupported_source_root")
	}
	if max < 1 {
		return nil, nil, nil, true, nil
	}
	files := make([]string, 0)
	skipped := make([]string, 0)
	// Boundary exclusions are not capture errors: the caller states them as exclusions.
	excluded := make([]string, 0)
	limited := false
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			skipped = append(skipped, path)
			return nil
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			skipped = append(skipped, path)
			return nil
		}
		if entry.IsDir() {
			if excludedPath(path) {
				excluded = append(excluded, path)
				return filepath.SkipDir
			}
			return nil
		}
		if excludedPath(path) {
			excluded = append(excluded, path)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			skipped = append(skipped, path)
			return nil
		}
		if !info.Mode().IsRegular() {
			skipped = append(skipped, path)
			return nil
		}
		if !supportedFile(path) {
			return nil
		}
		if len(files) >= max {
			limited = true
			return filepath.SkipAll
		}
		files = append(files, path)
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return nil, skipped, excluded, limited, err
	}
	sort.Strings(files)
	sort.Strings(skipped)
	sort.Strings(excluded)
	return files, skipped, excluded, limited, nil
}

/** Whether one entry of a walked tree belongs to the trust boundary. */
func excludedPath(path string) bool {
	return credentialSegment(filepath.Base(path)) || configSegment(filepath.Base(path)) || opencodeHistoryPath(path)
}

func supportedFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".jsonl" || ext == ".json"
}
func validateNoSymlinkComponents(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("source_path_outside_root")
	}
	rootInfo, err := os.Lstat(rootAbs)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("source_root_symlink")
	}
	if rel == "." {
		if !rootInfo.Mode().IsRegular() && !rootInfo.IsDir() {
			return errors.New("unsupported_source_root")
		}
		return nil
	}
	if !rootInfo.IsDir() {
		return errors.New("source_path_outside_root")
	}
	current := rootAbs
	parts := strings.Split(rel, string(os.PathSeparator))
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return errors.New("invalid_source_path")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("source_path_symlink")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return errors.New("source_path_not_directory")
		}
	}
	return nil
}

func validateSourceRoot(raw string) (string, *APIError) {
	if strings.TrimSpace(raw) == "" || strings.ContainsRune(raw, '\x00') {
		return "", &APIError{Status: http.StatusBadRequest, Code: "invalid_path", Message: "La ruta de la fuente es obligatoria."}
	}
	abs, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", &APIError{Status: http.StatusBadRequest, Code: "invalid_path", Message: "La ruta de la fuente no es válida."}
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", &APIError{Status: http.StatusBadRequest, Code: "source_unavailable", Message: "La ruta explícita no existe o no es accesible."}
	}
	if filepath.Clean(canonical) != abs {
		return "", &APIError{Status: http.StatusBadRequest, Code: "unsupported_source", Message: "La ruta explícita no puede atravesar symlinks."}
	}
	// Nothing of a credential, key or tool configuration tree is opened, not even to count
	// entries: the root itself is refused before its content is read.
	if apiErr := sourceBoundaryError(abs); apiErr != nil {
		return "", apiErr
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", &APIError{Status: http.StatusBadRequest, Code: "source_unavailable", Message: "La ruta explícita no existe o no es accesible."}
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && (!info.Mode().IsRegular() || !supportedFile(abs))) {
		return "", &APIError{Status: http.StatusBadRequest, Code: "unsupported_source", Message: "La fuente debe ser un directorio real o un archivo JSON/JSONL regular, no un symlink."}
	}
	return abs, nil
}

/** Why a root is refused: a secret name in it or in an ancestor, or a config name in the root
 *  itself; a source that merely lives under a `.config` directory stays accepted. */
func sourceBoundaryError(abs string) *APIError {
	if credentialSegment(filepath.Base(abs)) || configSegment(filepath.Base(abs)) {
		return &APIError{Status: http.StatusBadRequest, Code: "sensitive_source", Message: "La fuente no puede ser credenciales, claves ni configuración de herramientas."}
	}
	for _, segment := range strings.Split(filepath.Dir(abs), string(os.PathSeparator)) {
		if credentialSegment(segment) {
			return &APIError{Status: http.StatusBadRequest, Code: "sensitive_source", Message: "La fuente no puede estar dentro de credenciales, claves ni tokens."}
		}
	}
	if opencodeHistoryPath(abs) {
		return &APIError{Status: http.StatusBadRequest, Code: "opencode_history_not_supported", Message: "El historial de OpenCode no está soportado en esta entrega y no se ingiere con otro adaptador."}
	}
	return nil
}

func supportedAdapter(adapter Adapter) bool {
	return adapter == AdapterCodex || adapter == AdapterClaude || adapter == AdapterPi || adapter == AdapterOMP || adapter == AdapterCanonical
}

func (m *Module) loadSource(ctx context.Context, id string) (SourceDTO, error) {
	var source SourceDTO
	var adapter, coverageJSON, lastError, status string
	var enabled, limited int
	var files, changed, scanErrors int
	var lastCapture, lastScanAt sql.NullFloat64
	err := m.db.QueryRowContext(ctx, `SELECT id,path,adapter,enabled,created,last_capture,last_scan_at,last_scan_status,last_scan_files,last_scan_changed,last_scan_errors,last_scan_limited,coverage_json,last_error FROM sources WHERE id=?`, id).Scan(&source.ID, &source.Path, &adapter, &enabled, &source.Created, &lastCapture, &lastScanAt, &status, &files, &changed, &scanErrors, &limited, &coverageJSON, &lastError)
	if err != nil {
		return source, err
	}
	source.Adapter, source.Enabled = Adapter(adapter), enabled != 0
	source.LastCapture, source.LastScanAt = nullFloat(lastCapture), nullFloat(lastScanAt)
	source.LastScan = LastScan{Status: status, Files: files, Changed: changed, Errors: scanErrors, Limited: limited != 0}
	if source.LastScanAt != nil {
		source.LastScan.FinishedAt = *source.LastScanAt
	}
	source.Error = lastError
	if err := json.Unmarshal([]byte(coverageJSON), &source.Coverage); err != nil {
		source.Coverage = CoverageSummary{Status: "unknown"}
	}
	return source, nil
}

func (m *Module) loadSourceRecord(ctx context.Context, id string) (sourceRecord, error) {
	var source sourceRecord
	var adapter string
	var enabled int
	err := m.db.QueryRowContext(ctx, `SELECT id,path,adapter,enabled,created FROM sources WHERE id=?`, id).Scan(&source.ID, &source.Path, &adapter, &enabled, &source.Created)
	source.Adapter, source.Enabled = Adapter(adapter), enabled != 0
	return source, err
}

func transcriptSessionID(events []Event, fallback string) string {
	fallback = strings.TrimSpace(fallback)
	if !safeSessionID(fallback) {
		fallback = "conversation-" + hashShort([]byte(fallback), 20)
	}
	candidate := ""
	for _, event := range events {
		sessionID := strings.TrimSpace(stringValue(event["session_id"]))
		if sessionID == "" {
			continue
		}
		if !safeSessionID(sessionID) {
			return fallback
		}
		if candidate == "" {
			candidate = sessionID
		} else if candidate != sessionID {
			return fallback
		}
	}
	if candidate == "" {
		return fallback
	}
	return candidate
}

func safeSessionID(value string) bool {
	if value == "" || len(value) > 512 || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func (m *Module) sessionIDFor(sourceID, candidate string) (string, error) {
	var owner string
	err := m.db.QueryRow(`SELECT source_id FROM sessions WHERE id=?`, candidate).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || err == nil && owner == sourceID {
		return candidate, nil
	}
	if err != nil {
		return "", err
	}
	for _, suffixLength := range []int{12, 24, 32, 40} {
		base := candidate
		if maximum := 512 - suffixLength - 1; len(base) > maximum {
			base = base[:maximum]
		}
		id := base + "-" + hashShort([]byte(sourceID+"\x00"+candidate), suffixLength)
		err := m.db.QueryRow(`SELECT source_id FROM sessions WHERE id=?`, id).Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) || err == nil && owner == sourceID {
			return id, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("session_id_collision")
}

func (m *Module) updateJobProgress(jobID string, progress ScanResult) {
	_, _ = m.db.Exec(`UPDATE jobs SET files=?,changed=?,errors=?,max_files=?,current_file=?,limited=?,updated=? WHERE id=? AND status='running'`, progress.Files, progress.Changed, progress.Errors, progress.MaxFiles, progress.CurrentFile, boolInt(progress.Limited), nowSeconds(), jobID)
}

func (m *Module) finishJob(jobID, status string, result *ScanResult, message string) {
	var resultJSON any
	if result != nil {
		resultJSON = mustJSON(result)
	}
	_, _ = m.db.Exec(`UPDATE jobs SET status=?,result_json=?,error=?,files=coalesce(?,files),changed=coalesce(?,changed),errors=coalesce(?,errors),max_files=coalesce(?,max_files),current_file=coalesce(?,current_file),limited=coalesce(?,limited),updated=? WHERE id=?`, status, resultJSON, message, scanInt(result, func(x ScanResult) int { return x.Files }), scanInt(result, func(x ScanResult) int { return x.Changed }), scanInt(result, func(x ScanResult) int { return x.Errors }), scanInt(result, func(x ScanResult) int { return x.MaxFiles }), scanString(result, func(x ScanResult) string { return x.CurrentFile }), scanBool(result, func(x ScanResult) bool { return x.Limited }), nowSeconds(), jobID)
	m.notifyTaskScheduler()
}

func (m *Module) setSourceError(sourceID, message string) {
	if len(message) > 1000 {
		message = message[:1000]
	}
	_, _ = m.db.Exec(`UPDATE sources SET last_error=? WHERE id=?`, message, sourceID)
}

func (m *Module) getJob(w http.ResponseWriter, r *http.Request, rawID string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var job ScanJobDTO
	var resultJSON sql.NullString
	var errText string
	var files, changed, scanErrors, maxFiles, limited int
	var current string
	err := m.db.QueryRowContext(r.Context(), `SELECT id,kind,subject,status,files,changed,errors,max_files,current_file,limited,result_json,error,created,updated FROM jobs WHERE id=?`, id).Scan(&job.JobID, &job.Kind, &job.Subject, &job.Status, &files, &changed, &scanErrors, &maxFiles, &current, &limited, &resultJSON, &errText, &job.Created, &job.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "job_not_found", Message: "No se encontró el trabajo."})
		return
	}
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	job.Progress = ScanResult{Files: files, Changed: changed, Errors: scanErrors, MaxFiles: maxFiles, CurrentFile: current, Limited: limited != 0}
	job.Error = errText
	if resultJSON.Valid {
		var result ScanResult
		if err := json.Unmarshal([]byte(resultJSON.String), &result); err == nil {
			job.Result = &result
		}
	}
	writeJSON(w, http.StatusOK, job)
}

func (m *Module) cancelJob(w http.ResponseWriter, r *http.Request, rawID string) {
	id, apiErr := pathValue(rawID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var status string
	err := m.db.QueryRowContext(r.Context(), `SELECT status FROM jobs WHERE id=?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "job_not_found", Message: "No se encontró el trabajo."})
		return
	}
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if status == "queued" || status == "running" {
		m.cancelJobs(id)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": id, "status": status, "reused": status != "queued" && status != "running"})
}

func (m *Module) updateLastScan(sourceID string, result ScanResult, status string) {
	_, _ = m.db.Exec(`UPDATE sources SET last_scan_at=?,last_scan_status=?,last_scan_files=?,last_scan_changed=?,last_scan_errors=?,last_scan_max_files=?,last_scan_current_file=?,last_scan_limited=?,coverage_json=?,last_error=CASE WHEN ?='failed' THEN 'scan_failed' ELSE '' END WHERE id=?`, nowSeconds(), status, result.Files, result.Changed, result.Errors, result.MaxFiles, result.CurrentFile, boolInt(result.Limited), mustJSON(CoverageSummary{Status: scanStatus(&result), IssueCount: result.Errors}), status, sourceID)
}

func scanStatus(result *ScanResult) string {
	if result.Errors > 0 || result.Limited {
		return "partial"
	}
	return "complete"
}

func titleFromEvents(events []Event, fallback string) string {
	for _, event := range events {
		if stringField(event["role"]) == "user" {
			text := strings.TrimSpace(stringField(event["text"]))
			if text != "" {
				return truncateRunes(text, 120)
			}
		}
	}
	return truncateRunes(fallback, 120)
}

func summaryFromCoverage(coverage Coverage, activity float64, path string) CoverageSummary {
	issues := append([]Issue(nil), coverage.Issues...)
	for index := range issues {
		issues[index].FileName = filepath.Base(path)
	}
	return CoverageSummary{Status: coverage.Status, EventCount: coverage.EventCount, IssueCount: coverage.IssueCount, Issues: issues, BlockingIssueCount: coverage.BlockingIssueCount, SemanticIssueCount: coverage.SemanticIssueCount, Bytes: coverage.Bytes, ActivityAt: activity, NormalizerVersion: coverage.NormalizerVersion, RedactionVersion: RedactionVersion, RawSHA256: coverage.RawSHA256, AppendPending: coverage.AppendPending}
}

func transcriptHostID(events []Event) (string, error) {
	hostID := ""
	for _, event := range events {
		value := stringField(event["host_id"])
		if value == "" || value == "local" {
			continue
		}
		if hostID != "" && hostID != value {
			return "", errors.New("multiple_host_scopes_in_transcript")
		}
		hostID = value
	}
	return hostID, nil
}

func nullFloat(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	out := value.Float64
	return &out
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func mustJSON(value any) string {
	encoded, err := jsonText(value)
	if err != nil {
		return "{}"
	}
	return encoded
}

func stringField(value any) string { text, _ := value.(string); return text }

func intField(value any) int {
	switch item := value.(type) {
	case int:
		return item
	case int64:
		return int(item)
	case float64:
		return int(item)
	case json.Number:
		parsed, _ := item.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func eventBool(value any) bool { result, _ := value.(bool); return result }

func scanInt(result *ScanResult, f func(ScanResult) int) any {
	if result == nil {
		return nil
	}
	return f(*result)
}
func scanString(result *ScanResult, f func(ScanResult) string) any {
	if result == nil {
		return nil
	}
	return f(*result)
}
func scanBool(result *ScanResult, f func(ScanResult) bool) any {
	if result == nil {
		return nil
	}
	return boolInt(f(*result))
}

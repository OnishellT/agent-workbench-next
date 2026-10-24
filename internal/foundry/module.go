// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"workbench/internal/harness"
)

var errSourceRevoked = errors.New("source_revoked")

// ErrShutdownUnconfirmed means shutdown began but the caller's deadline expired before active jobs drained.
var ErrShutdownUnconfirmed = errors.New("foundry shutdown not confirmed")

// New opens Foundry's private database and marks jobs interrupted by a previous
// process exit. It never discovers or scans a source during construction.
func New(options Options) (*Module, error) {
	if options.StateDir == "" {
		return nil, errors.New("Foundry requires the shared state directory")
	}
	stateDir, err := filepath.Abs(options.StateDir)
	if err != nil {
		return nil, err
	}
	db, err := openStore(stateDir)
	if err != nil {
		return nil, err
	}
	engines := options.AgentEngines
	if engines == nil {
		engines, err = harness.New(harness.Options{StateDir: stateDir})
		if err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	closeCtx, cancel := context.WithCancel(context.Background())
	module := &Module{
		db:               db,
		stateDir:         stateDir,
		agents:           engines,
		projectRoots:     options.ProjectRoots,
		remoteMemuRunner: options.RemoteMemuRunner,
		taskWake:         make(chan struct{}, 1),
		captureWake:      make(chan struct{}, 1),
		hostScope:        options.HostScope,
		jobs:             make(map[string]context.CancelFunc),
		closeDone:        make(chan struct{}),
		closeCtx:         closeCtx,
		cancel:           cancel,
	}
	if _, err := db.Exec(`UPDATE jobs SET status='interrupted',error='process_restarted',updated=? WHERE status IN ('queued','running')`, nowSeconds()); err != nil {
		cancel()
		_ = db.Close()
		return nil, err
	}
	if err := recoverLearningEvaluations(db); err != nil {
		cancel()
		_ = db.Close()
		return nil, err
	}
	if err := module.initTaskConfig(); err != nil {
		cancel()
		_ = db.Close()
		return nil, err
	}
	module.wg.Add(1)
	go module.autoCaptureScheduler()
	return module, nil
}

// Close stops HTTP and job admission, cancels background workers, and drains active
// handlers and jobs before closing the private DB. A deadline returns ErrShutdownUnconfirmed
// while the drain continues and the DB remains open.
func (m *Module) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		m.cancel()
		for _, cancel := range m.jobs {
			cancel()
		}
		go m.finishClose()
	}
	done := m.closeDone
	m.mu.Unlock()

	select {
	case <-done:
		return m.closeErr
	case <-ctx.Done():
		select {
		case <-done:
			return m.closeErr
		default:
			return fmt.Errorf("%w: %w", ErrShutdownUnconfirmed, ctx.Err())
		}
	}
}

// jobIDs runs a best-effort "SELECT id ..." used to find jobs to cancel: a query
// error yields no ids, rows that fail to scan are skipped, and the rows are
// closed before returning (the store has a single connection).
func jobIDs(ctx context.Context, q rowQuerier, query string, args ...any) []string {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// cancelJobsLocked cancels the running workers registered under ids; callers hold m.mu.
func (m *Module) cancelJobsLocked(ids ...string) {
	for _, id := range ids {
		if cancel := m.jobs[id]; cancel != nil {
			cancel()
		}
	}
}

func (m *Module) cancelJobs(ids ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelJobsLocked(ids...)
}

// forgetJob drops a finished worker's cancel func from the registry.
func (m *Module) forgetJob(id string) {
	m.mu.Lock()
	delete(m.jobs, id)
	m.mu.Unlock()
}

func (m *Module) finishClose() {
	m.wg.Wait()
	closeErr := m.db.Close()
	m.mu.Lock()
	m.closeErr = closeErr
	close(m.closeDone)
	m.mu.Unlock()
}

func (m *Module) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] != "api" {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Ruta no encontrada."})
		return
	}
	switch parts[1] {
	case "conversations":
		m.serveConversations(w, r, parts[2:])
	case "tasks":
		m.serveTasks(w, r, parts[2:])
	case "foundry":
		m.serveFoundry(w, r, parts[2:])
	default:
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Ruta no encontrada."})
	}
}

func (m *Module) serveConversations(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		m.listConversations(w, r)
		return
	}
	if parts[0] == "sources" {
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				m.listSources(w, r)
			case http.MethodPost:
				m.createSource(w, r)
			default:
				methodNotAllowed(w, http.MethodGet, http.MethodPost)
			}
			return
		}
		if parts[1] == "discover" && len(parts) == 2 {
			if r.Method != http.MethodPost {
				methodNotAllowed(w, http.MethodPost)
				return
			}
			m.discoverSources(w, r)
			return
		}
		if len(parts) == 3 && parts[2] == "scan" {
			if r.Method != http.MethodPost {
				methodNotAllowed(w, http.MethodPost)
				return
			}
			m.queueSourceScan(w, r, parts[1])
			return
		}
		if len(parts) == 2 {
			if r.Method != http.MethodPatch {
				methodNotAllowed(w, http.MethodPatch)
				return
			}
			m.patchSource(w, r, parts[1])
			return
		}
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Ruta no encontrada."})
		return
	}
	if parts[0] == "scan" && len(parts) == 1 {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		m.queueScan(w, r)
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		m.conversationDetail(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "revisions" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		m.conversationRevisions(w, r, parts[0])
		return
	}
	if len(parts) == 3 && parts[1] == "revisions" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		m.conversationRevision(w, r, parts[0], parts[2])
		return
	}
	writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Ruta no encontrada."})
}

func (m *Module) serveTasks(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 1 && parts[0] == "config" {
		m.taskConfig(w, r)
		return
	}
	if len(parts) > 0 && parts[0] == "proposals" {
		switch {
		case len(parts) == 1:
			if r.Method != http.MethodPost {
				methodNotAllowed(w, http.MethodPost)
				return
			}
			legacyInferenceGone(w)
		case len(parts) == 2:
			if r.Method != http.MethodGet {
				methodNotAllowed(w, http.MethodGet)
				return
			}
			m.taskProposal(w, r, parts[1])
		case len(parts) == 3 && parts[2] == "accept":
			if r.Method != http.MethodPost {
				methodNotAllowed(w, http.MethodPost)
				return
			}
			m.acceptTaskProposal(w, r, parts[1])
		default:
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Ruta no encontrada."})
		}
		return
	}
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			m.listTasks(w, r)
		case http.MethodPost:
			m.createTask(w, r)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		m.taskDetail(w, r, parts[0])
		return
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "completion":
			if r.Method != http.MethodPost {
				methodNotAllowed(w, http.MethodPost)
				return
			}
			m.setTaskCompletion(w, r, parts[0])
		case "label":
			if r.Method != http.MethodPost {
				methodNotAllowed(w, http.MethodPost)
				return
			}
			m.setTaskLabel(w, r, parts[0])
		case "feedback":
			if r.Method != http.MethodPost {
				methodNotAllowed(w, http.MethodPost)
				return
			}
			m.setTaskFeedback(w, r, parts[0])
		case "history":
			if r.Method != http.MethodGet {
				methodNotAllowed(w, http.MethodGet)
				return
			}
			m.taskHistory(w, r, parts[0])
		default:
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Ruta no encontrada."})
		}
		return
	}
	writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Ruta no encontrada."})
}

func (m *Module) serveFoundry(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 1 && parts[0] == "example" {
		switch r.Method {
		case http.MethodGet:
			m.exampleStatus(w, r)
		case http.MethodPost:
			m.runExample(w, r)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(parts) == 2 && parts[0] == "jobs" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		if m.foundryJobRoute(w, r, parts[1], "") {
			return
		}
		m.getJob(w, r, parts[1])
		return
	}
	if len(parts) == 3 && parts[0] == "jobs" && parts[2] == "cancel" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		if m.foundryJobRoute(w, r, parts[1], "cancel") {
			return
		}
		m.cancelJob(w, r, parts[1])
		return
	}
	if len(parts) == 3 && parts[0] == "jobs" && parts[2] == "retry" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		if m.foundryJobRoute(w, r, parts[1], "retry") {
			return
		}
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "job_not_retryable", Message: "Este trabajo no se puede reintentar en su estado actual."})
		return
	}
	if len(parts) > 0 && parts[0] == "memu" {
		m.serveMemu(w, r, parts[1:])
		return
	}
	m.serveLearning(w, r, parts)
}

func decodeBody(w http.ResponseWriter, r *http.Request, destination any) *APIError {
	r.Body = http.MaxBytesReader(w, r.Body, maxHTTPBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return &APIError{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "El cuerpo supera el límite permitido."}
		}
		return &APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "El cuerpo JSON no es válido."}
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return &APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "El cuerpo debe contener un solo objeto JSON."}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, err *APIError) {
	if err == nil {
		err = &APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Error interno."}
	}
	if err.Status == 0 {
		err.Status = http.StatusInternalServerError
	}
	writeJSON(w, err.Status, map[string]string{"error": err.Message, "code": err.Code})
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeAPIError(w, &APIError{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "Método no permitido."})
}

func queryLimit(r *http.Request) (int, int, *APIError) {
	limit := defaultPageLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return 0, 0, &APIError{Status: http.StatusBadRequest, Code: "invalid_limit", Message: "El límite debe ser un entero positivo."}
		}
		if value > maxPageLimit {
			value = maxPageLimit
		}
		limit = value
	}
	offset := 0
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return 0, 0, &APIError{Status: http.StatusBadRequest, Code: "invalid_cursor", Message: "El cursor debe ser un entero no negativo."}
		}
		offset = value
	}
	return limit, offset, nil
}

func pathValue(raw string) (string, *APIError) {
	value, err := url.PathUnescape(raw)
	if err != nil || value == "" || strings.ContainsAny(value, "\\\x00") {
		return "", &APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "Identificador inválido."}
	}
	return value, nil
}

func newID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func nowSeconds() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func internalError(err error) *APIError {
	return &APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: fmt.Sprintf("Error interno de Foundry: %v", err)}
}

func legacyInferenceGone(w http.ResponseWriter) {
	writeAPIError(w, &APIError{
		Status:  http.StatusGone,
		Code:    "legacy_inference_disabled",
		Message: "La generación legacy está deshabilitada. Usa Aprendizajes > memU; las lecturas y decisiones históricas siguen disponibles.",
	})
}

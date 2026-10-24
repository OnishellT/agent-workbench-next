// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"workbench/internal/harness"
)

const (
	memuMaxJobs       = 10
	memuMaxJobBytes   = 256 << 10
	memuMaxFileBytes  = 64 << 10
	memuProcessBudget = 30 * time.Minute
)

var memuPreparedPattern = regexp.MustCompile(`prepared\s+(\d+)\s+session`)

type memuConfigRequest struct {
	HostID  *string `json:"host_id"`
	Enabled *bool   `json:"enabled"`
	Engine  *string `json:"engine"`
	Profile *string `json:"profile"`
}

type memuStoredState struct {
	HostID     string       `json:"host_id"`
	Enabled    bool         `json:"enabled"`
	Engine     string       `json:"engine"`
	Profile    string       `json:"profile"`
	Phase      string       `json:"phase"`
	LastError  string       `json:"last_error,omitempty"`
	PreparedAt float64      `json:"prepared_at,omitempty"`
	Published  bool         `json:"published,omitempty"`
	Jobs       []memuJobDTO `json:"jobs"`
}

type memuEvidenceRef struct {
	SessionID string `json:"session_id"`
	SourceID  string `json:"source_id"`
	Revision  string `json:"revision"`
	Host      string `json:"host"`
}

type memuJobDTO struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Status  string `json:"status,omitempty"`
	Error   string `json:"error,omitempty"`
}

type memuFileDTO struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type memuStateDTO struct {
	HostID     string        `json:"host_id"`
	Installed  bool          `json:"installed"`
	Version    string        `json:"version"`
	Configured bool          `json:"configured"`
	Enabled    bool          `json:"enabled"`
	Engine     string        `json:"engine"`
	Profile    string        `json:"profile"`
	Phase      string        `json:"phase"`
	LastError  string        `json:"last_error"`
	Jobs       []memuJobDTO  `json:"jobs"`
	Files      []memuFileDTO `json:"files"`
}

func (m *Module) serveMemu(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			m.getMemuState(w, r)
		case http.MethodPut:
			m.putMemuConfig(w, r)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPut)
		}
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		switch parts[0] {
		case "commit":
			m.commitMemu(w, r)
		case "prepare":
			m.prepareMemu(w, r)
		case "process":
			m.processMemu(w, r)
		default:
			writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Ruta no encontrada."})
		}
		return
	}
	writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Ruta no encontrada."})
}

func (m *Module) getMemuState(w http.ResponseWriter, r *http.Request) {
	settings, _, apiErr := m.loadMemuSettings()
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	state, apiErr := m.memuState(r.Context(), settings)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (m *Module) putMemuConfig(w http.ResponseWriter, r *http.Request) {
	var request memuConfigRequest
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if request.Enabled == nil && request.Engine == nil && request.Profile == nil && request.HostID == nil {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "La configuración debe contener al menos un campo."})
		return
	}

	if !m.memuMu.TryLock() {
		memuBusy(w)
		return
	}
	defer m.memuMu.Unlock()
	settings, base, apiErr := m.loadMemuSettings()
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if request.HostID != nil {
		host := strings.TrimSpace(*request.HostID)
		if host == "" {
			host = "local"
		}
		if host != "local" {
			if m.hostScope == nil {
				writeAPIError(w, &APIError{Status: 409, Code: "host_scope_denied", Message: "Registro de hosts no disponible."})
				return
			}
			if _, err := m.hostScope.Get(r.Context(), host); err != nil {
				writeAPIError(w, &APIError{Status: 400, Code: "host_scope_denied", Message: "Host no registrado."})
				return
			}
		}
		settings.HostID = host
	}
	if request.Enabled != nil {
		settings.Enabled = *request.Enabled
	}
	if request.Engine != nil {
		engine := strings.TrimSpace(*request.Engine)
		if engine != harness.EngineCodex && engine != harness.EngineOMP {
			writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "memu_engine_invalid", Message: "memU solo puede usar los motores nativos codex u omp."})
			return
		}
		if engine != settings.Engine {
			settings.Engine = engine
			settings.Profile = ""
		}
	}
	if request.Profile != nil {
		settings.Profile = strings.TrimSpace(*request.Profile)
	}
	if settings.Engine == harness.EngineCodex && settings.Profile != "" && settings.Profile != "default" {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "memu_profile_invalid", Message: "Codex conserva su perfil nativo; deja el perfil vacío."})
		return
	}
	if settings.Engine == harness.EngineOMP && settings.Profile != "" && settings.Profile != "default" && settings.Profile != "personal" && settings.Profile != "work" {
		writeAPIError(w, &APIError{Status: http.StatusBadRequest, Code: "memu_profile_invalid", Message: "El perfil OMP debe ser default, personal o work."})
		return
	}
	if apiErr := m.writeMemuSettings(base, settings); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	state, apiErr := m.memuState(r.Context(), settings)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (m *Module) prepareMemu(w http.ResponseWriter, r *http.Request) {
	var request struct{}
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}

	if !m.memuMu.TryLock() {
		memuBusy(w)
		return
	}
	defer m.memuMu.Unlock()
	settings, base, apiErr := m.loadMemuSettings()
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if !settings.Enabled {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "memu_disabled", Message: "Activa memU antes de preparar una exportación."})
		return
	}
	if len(settings.Jobs) > 0 && settings.Phase != "ready" && m.memuManifestAuthorized(r.Context(), base) {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "memu_jobs_pending", Message: "Procesa los trabajos pendientes antes de preparar otro lote."})
		return
	}
	binary, apiErr := requireMemuBinary()
	if apiErr != nil {
		m.rememberMemuFailure(base, &settings, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	if apiErr := requireMemuConfiguration(); apiErr != nil {
		m.rememberMemuFailure(base, &settings, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}

	ctx, cancel := m.memuRequestContext(r.Context())
	defer cancel()
	if err := m.reconcileMemuRollbacks(ctx, base); err != nil {
		writeAPIError(w, &APIError{Status: 409, Code: "memu_rollback_pending", Message: "Hay una restauración de memoria pendiente: " + safeLearningError(err.Error(), 500)})
		return
	}
	settings.Published = false
	settings.Phase = "preparing"
	if apiErr := m.writeMemuSettings(base, settings); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	exportDir := filepath.Join(base, "export")
	if err := clearMemuProposals(base); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	_, apiErr = m.exportMemuEvidence(ctx, exportDir)
	if apiErr != nil {
		m.rememberMemuFailure(base, &settings, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}

	prepareCtx, prepareCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer prepareCancel()
	result, err := runProviderProcess(prepareCtx, binary, []string{
		"prepare",
		"--base-dir", base,
		"--session-dir", exportDir,
		"--max-jobs", strconv.Itoa(memuMaxJobs),
	}, base, "", memuChildEnvironment())
	if err != nil {
		apiErr := &APIError{Status: http.StatusBadGateway, Code: "memu_prepare_failed", Message: "memU no pudo preparar los trabajos: " + safeLearningError(err.Error(), 800)}
		m.rememberMemuFailure(base, &settings, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	sessions := 0
	if match := memuPreparedPattern.FindSubmatch(result.stdout); len(match) == 2 {
		sessions, _ = strconv.Atoi(string(match[1]))
	}
	jobs, err := readMemuJobs(base, sessions)
	if err == nil {
		jobs, err = appendMemuSynthesisJob(base, jobs)
	}
	if err != nil {
		apiErr := internalError(err)
		m.rememberMemuFailure(base, &settings, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	settings.Jobs = jobs
	settings.PreparedAt = nowSeconds()
	settings.Published = false
	settings.LastError = ""
	if len(jobs) == 0 {
		settings.Phase = "idle"
	} else {
		settings.Phase = "prepared"
	}
	if apiErr := m.writeMemuSettings(base, settings); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	state, apiErr := m.memuState(r.Context(), settings)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (m *Module) processMemu(w http.ResponseWriter, r *http.Request) {
	var request struct{}
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}

	if !m.memuMu.TryLock() {
		memuBusy(w)
		return
	}
	defer m.memuMu.Unlock()
	settings, base, apiErr := m.loadMemuSettings()
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if !settings.Enabled {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "memu_disabled", Message: "Activa memU antes de procesar propuestas."})
		return
	}
	if _, apiErr := requireMemuBinary(); apiErr != nil {
		m.rememberMemuFailure(base, &settings, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	if apiErr := requireMemuConfiguration(); apiErr != nil {
		m.rememberMemuFailure(base, &settings, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	if settings.PreparedAt == 0 {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "memu_not_prepared", Message: "Prepara memU antes de procesar sus trabajos."})
		return
	}
	if len(settings.Jobs) == 0 {
		settings.Phase = "ready"
		settings.LastError = ""
		if apiErr := m.writeMemuSettings(base, settings); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		state, apiErr := m.memuState(r.Context(), settings)
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		writeJSON(w, http.StatusOK, state)
		return
	}

	if !m.memuManifestAuthorized(r.Context(), base) {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "memu_evidence_revoked", Message: "La evidencia cambió o fue revocada. Prepara otro lote."})
		return
	}
	var synthesisErr error
	settings.Jobs, synthesisErr = appendMemuSynthesisJob(base, settings.Jobs)
	if synthesisErr != nil {
		writeAPIError(w, internalError(synthesisErr))
		return
	}
	var runJob func(context.Context, string) error
	if settings.HostID == "local" || settings.HostID == "" {
		command, err := m.agents.NativeCommand(harness.Spec{Engine: settings.Engine, Profile: settings.Profile, Permission: harness.PermissionWorkspaceWrite})
		if err != nil {
			writeAPIError(w, &APIError{Status: 409, Code: "memu_engine_unavailable", Message: safeLearningError(err.Error(), 500)})
			return
		}
		command.Env = memuCLIEnvironment(replaceCommandEnv(command.Env, "PWD", base))
		runJob = func(ctx context.Context, prompt string) error {
			_, err := runProviderProcess(ctx, command.Path, command.Args, base, prompt, command.Env)
			return err
		}
	} else {
		if m.remoteMemuRunner == nil {
			writeAPIError(w, &APIError{Status: 409, Code: "memu_engine_unavailable", Message: "Procesamiento SSH no disponible."})
			return
		}
		runJob = func(ctx context.Context, prompt string) error {
			return m.remoteMemuRunner(ctx, settings.HostID, settings.Engine, settings.Profile, base, prompt)
		}
	}

	ctx, cancel := m.memuRequestContext(r.Context())
	defer cancel()
	processCtx, processCancel := context.WithTimeout(ctx, memuProcessBudget)
	defer processCancel()
	stopWatch := m.watchMemuEvidence(processCtx, processCancel, base)
	defer stopWatch()
	settings.Published = false
	settings.Phase = "processing"
	settings.LastError = ""
	for index := range settings.Jobs {
		if settings.Jobs[index].Status == "succeeded" {
			continue
		}
		settings.Jobs[index].Status = "queued"
		settings.Jobs[index].Error = ""
	}
	if apiErr := m.writeMemuSettings(base, settings); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}

	for index := range settings.Jobs {
		job := &settings.Jobs[index]
		if job.Status == "succeeded" {
			continue
		}
		jobPath, pathErr := safeMemuJobPath(base, job.Path)
		if pathErr != nil {
			m.failMemuJob(base, &settings, index, pathErr.Error())
			writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "memu_job_invalid", Message: "El trabajo de memU no es seguro: " + pathErr.Error()})
			return
		}
		instruction, readErr := readMemuFile(jobPath, memuMaxJobBytes)
		if readErr != nil {
			m.failMemuJob(base, &settings, index, readErr.Error())
			writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "memu_job_invalid", Message: "No se pudo leer el trabajo de memU: " + safeLearningError(readErr.Error(), 500)})
			return
		}
		job.Status = "running"
		if apiErr := m.writeMemuSettings(base, settings); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		runErr := runJob(processCtx, memuProcessPrompt(base, instruction))
		if runErr != nil {
			message := safeLearningError(runErr.Error(), 800)
			m.failMemuJob(base, &settings, index, message)
			writeAPIError(w, &APIError{Status: http.StatusBadGateway, Code: "memu_process_failed", Message: "El agente nativo no terminó el trabajo: " + message})
			return
		}
		if !m.memuManifestAuthorized(processCtx, base) {
			m.failMemuJob(base, &settings, index, "La evidencia fue revocada.")
			writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "memu_evidence_revoked", Message: "La evidencia cambió o fue revocada."})
			return
		}
		job.Status = "succeeded"
		job.Error = ""
		if apiErr := m.writeMemuSettings(base, settings); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
	}
	settings.Published = true
	settings.Phase = "ready"
	settings.LastError = ""
	if apiErr := m.writeMemuSettings(base, settings); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	state, apiErr := m.memuState(r.Context(), settings)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (m *Module) memuState(ctx context.Context, settings memuStoredState) (memuStateDTO, *APIError) {
	base, err := m.memuBase()
	if err != nil {
		return memuStateDTO{}, internalError(err)
	}
	binary, installed, _ := memuBinary()
	configured := requireMemuConfiguration() == nil
	files := []memuFileDTO{}
	if m.memuProofAuthorized(ctx, settings) {
		files, err = memuProposalFiles(base)
		if err != nil {
			return memuStateDTO{}, internalError(err)
		}
	}
	jobs := append([]memuJobDTO(nil), settings.Jobs...)
	if jobs == nil {
		jobs = []memuJobDTO{}
	}
	if files == nil {
		files = []memuFileDTO{}
	}
	phase := settings.Phase
	if phase == "" {
		phase = "idle"
	}
	return memuStateDTO{
		HostID:     settings.HostID,
		Installed:  installed,
		Version:    memuVersion(binary),
		Configured: configured,
		Enabled:    settings.Enabled,
		Engine:     settings.Engine,
		Profile:    settings.Profile,
		Phase:      phase,
		LastError:  settings.LastError,
		Jobs:       jobs,
		Files:      files,
	}, nil
}

func (m *Module) memuEvidenceAuthorized(ctx context.Context, evidence []memuEvidenceRef) bool {
	if len(evidence) == 0 {
		return false
	}
	for _, ref := range evidence {
		var sourceID, revision, host string
		var enabled int
		err := m.db.QueryRowContext(ctx, `SELECT s.source_id,s.revision,s.host_id,src.enabled
			FROM sessions s JOIN sources src ON src.id=s.source_id WHERE s.id=?`, ref.SessionID).
			Scan(&sourceID, &revision, &host, &enabled)
		if err != nil || enabled == 0 || sourceID != ref.SourceID || revision != ref.Revision || normalizedMemuHost(host) != normalizedMemuHost(ref.Host) {
			return false
		}
	}
	return true
}

func (m *Module) memuManifestAuthorized(ctx context.Context, base string) bool {
	return m.memuEvidenceFileAuthorized(ctx, filepath.Join(base, "export", "manifest.json"))
}

func (m *Module) memuEvidenceFileAuthorized(ctx context.Context, path string) bool {
	raw, err := readMemuFile(path, 1<<20)
	data := []byte(raw)
	if err != nil {
		return false
	}
	var manifest memuExportManifest
	if json.Unmarshal(data, &manifest) != nil {
		return false
	}
	evidence := make([]memuEvidenceRef, 0, len(manifest.Sessions))
	for _, session := range manifest.Sessions {
		if normalizedMemuHost(session.Host) != "local" && (session.Identity == nil || m.checkHistoryIdentity(ctx, *session.Identity) != nil) {
			return false
		}
		evidence = append(evidence, memuEvidenceRef{
			SessionID: session.ID,
			SourceID:  session.SourceID,
			Revision:  session.Revision,
			Host:      session.Host,
		})
	}
	return m.memuEvidenceAuthorized(ctx, evidence)
}

func normalizedMemuHost(host string) string {
	if strings.TrimSpace(host) == "" {
		return "local"
	}
	return host
}

func memuBusy(w http.ResponseWriter) {
	writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "memu_busy", Message: "memU ya está preparando o procesando; espera a que termine."})
}

func (m *Module) loadMemuSettings() (memuStoredState, string, *APIError) {
	base, err := m.memuBase()
	if err != nil {
		return memuStoredState{}, "", internalError(err)
	}
	if err := ensurePrivateDir(base); err != nil {
		return memuStoredState{}, "", internalError(err)
	}
	settings := memuStoredState{HostID: "local", Engine: harness.EngineCodex, Phase: "idle", Jobs: []memuJobDTO{}}
	data, err := os.ReadFile(filepath.Join(base, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return settings, base, nil
	}
	if err != nil {
		return settings, base, internalError(err)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return settings, base, &APIError{Status: http.StatusInternalServerError, Code: "memu_state_invalid", Message: "El estado guardado de memU no es válido."}
	}
	if settings.HostID == "" {
		settings.HostID = "local"
	}
	if settings.Engine == "" {
		settings.Engine = harness.EngineCodex
	}
	if settings.Jobs == nil {
		settings.Jobs = []memuJobDTO{}
	}
	return settings, base, nil
}

func (m *Module) writeMemuSettings(base string, settings memuStoredState) *APIError {
	if settings.Jobs == nil {
		settings.Jobs = []memuJobDTO{}
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return internalError(err)
	}
	if err := writePrivateFile(filepath.Join(base, "settings.json"), append(data, '\n')); err != nil {
		return internalError(err)
	}
	return nil
}

func (m *Module) rememberMemuFailure(base string, settings *memuStoredState, message string) {
	settings.Phase = "failed"
	settings.LastError = safeLearningError(message, 1800)
	_ = m.writeMemuSettings(base, *settings)
}

func (m *Module) failMemuJob(base string, settings *memuStoredState, index int, message string) {
	settings.Jobs[index].Status = "failed"
	settings.Jobs[index].Error = safeLearningError(message, 800)
	m.rememberMemuFailure(base, settings, message)
}

func (m *Module) memuBase() (string, error) {
	if strings.TrimSpace(m.stateDir) == "" || !filepath.IsAbs(m.stateDir) {
		return "", errors.New("el estado de memU requiere una ruta absoluta")
	}
	return filepath.Join(m.stateDir, "memu"), nil
}

func (m *Module) memuRequestContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(m.closeCtx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

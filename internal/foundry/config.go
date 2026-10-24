// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"unicode/utf8"

	"workbench/internal/harness"
)

const (
	defaultTaskIdleSeconds   = 120
	defaultTaskInputChars    = 160000
	defaultTaskMaxCalls      = 36
	defaultTaskBudgetSeconds = 3600.0
)

type taskConfigPatchRequest struct {
	Config map[string]json.RawMessage `json:"config"`
}

func (m *Module) initTaskConfig() error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := nowSeconds()
	if _, err := tx.Exec(`INSERT OR IGNORE INTO task_settings(singleton,auto_tasks,task_idle_seconds,max_input_chars,max_calls,experiment_seconds,updated) VALUES(1,0,?,?,?,?,?)`, defaultTaskIdleSeconds, defaultTaskInputChars, defaultTaskMaxCalls, defaultTaskBudgetSeconds, now); err != nil {
		return err
	}
	defaults := []TaskProviderConfig{
		{Kind: "pi", Command: []string{"pi"}, Timeout: 180, Reasoning: "medium"},
		{Kind: "codex", Command: []string{"codex"}, Timeout: 180},
	}
	for i, role := range []string{"provider", "replay"} {
		command, err := jsonText(defaults[i].Command)
		if err != nil {
			return err
		}
		provider := defaults[i]
		if _, err := tx.Exec(`INSERT OR IGNORE INTO task_provider_configs(role,kind,command_json,model,timeout_seconds,real_inference,model_provider,reasoning,updated) VALUES(?,?,?,?,?,?,?,?,?)`,
			role, provider.Kind, command, provider.Model, provider.Timeout, boolInt(provider.RealInference), provider.Provider, provider.Reasoning, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Module) loadTaskConfig(ctx context.Context) (TaskConfig, error) {
	return loadTaskConfig(ctx, m.db)
}

type taskConfigQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadTaskConfig(ctx context.Context, query taskConfigQuerier) (TaskConfig, error) {
	var cfg TaskConfig
	var auto, autoDerive, autoEvaluate int
	if err := query.QueryRowContext(ctx, `SELECT auto_tasks,auto_derive,auto_evaluate,task_idle_seconds,poll_seconds,max_input_chars,repetitions,max_calls,experiment_seconds,min_quality_gain,max_time_ratio,min_efficiency_gain,max_source_bytes,max_files_per_scan,max_scan_seconds FROM task_settings WHERE singleton=1`).
		Scan(&auto, &autoDerive, &autoEvaluate, &cfg.TaskIdleSeconds, &cfg.PollSeconds, &cfg.MaxInputChars, &cfg.Repetitions, &cfg.MaxCalls, &cfg.ExperimentSeconds, &cfg.MinQualityGain, &cfg.MaxTimeRatio, &cfg.MinEfficiencyGain, &cfg.MaxSourceBytes, &cfg.MaxFilesPerScan, &cfg.MaxScanSeconds); err != nil {
		return cfg, err
	}
	cfg.AutoTasks = auto != 0
	cfg.AutoDerive = autoDerive != 0
	cfg.AutoEvaluate = autoEvaluate != 0
	cfg.AutoPromote = false
	provider, err := loadTaskProviderConfig(ctx, query, "provider")
	if err != nil {
		return cfg, err
	}
	replay, err := loadTaskProviderConfig(ctx, query, "replay")
	if err != nil {
		return cfg, err
	}
	cfg.Provider, cfg.Replay = provider, replay
	return cfg, nil
}

func loadTaskProviderConfig(ctx context.Context, query taskConfigQuerier, role string) (TaskProviderConfig, error) {
	var cfg TaskProviderConfig
	var kind, command string
	var real int
	if err := query.QueryRowContext(ctx, `SELECT kind,command_json,model,timeout_seconds,real_inference,model_provider,reasoning FROM task_provider_configs WHERE role=?`, role).
		Scan(&kind, &command, &cfg.Model, &cfg.Timeout, &real, &cfg.Provider, &cfg.Reasoning); err != nil {
		return cfg, err
	}
	cfg.Kind, cfg.RealInference = kind, real != 0
	if err := json.Unmarshal([]byte(command), &cfg.Command); err != nil || cfg.Command == nil {
		return TaskProviderConfig{}, errors.New("invalid stored provider command")
	}
	return cfg, nil
}

func (m *Module) taskConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := m.loadTaskConfig(r.Context())
		if err != nil {
			writeAPIError(w, internalError(err))
			return
		}
		cfg.AutoPromote = false
		writeJSON(w, http.StatusOK, taskConfigResponse(cfg, m.providerDiagnostics(cfg)))
	case http.MethodPatch:
		m.patchTaskConfig(w, r)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPatch)
	}
}

func (m *Module) patchTaskConfig(w http.ResponseWriter, r *http.Request) {
	var request taskConfigPatchRequest
	if apiErr := decodeBody(w, r, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if len(request.Config) == 0 {
		writeAPIError(w, badTaskConfig("config debe contener al menos un campo."))
		return
	}
	allowed := map[string]bool{"auto_tasks": true, "auto_derive": true, "auto_evaluate": true, "auto_promote": true, "poll_seconds": true, "task_idle_seconds": true, "max_input_chars": true, "repetitions": true, "max_calls": true, "experiment_seconds": true, "min_quality_gain": true, "max_time_ratio": true, "min_efficiency_gain": true, "max_source_bytes": true, "max_files_per_scan": true, "max_scan_seconds": true, "provider": true, "replay": true}
	fields := make([]string, 0, len(request.Config))
	for key := range request.Config {
		if !allowed[key] {
			writeAPIError(w, badTaskConfig("Campo de configuración desconocido: "+key))
			return
		}
		fields = append(fields, key)
	}
	sort.Strings(fields)

	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	defer tx.Rollback()
	cfg, err := loadTaskConfig(r.Context(), tx)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	oldAuto := cfg.AutoTasks
	oldProvider, _ := jsonText(cfg.Provider)
	providerChanged := false
	for key, raw := range request.Config {
		switch key {
		case "auto_tasks":
			if err := decodeConfigValue(raw, &cfg.AutoTasks); err != nil {
				writeAPIError(w, badTaskConfig("auto_tasks debe ser booleano."))
				return
			}
		case "auto_derive":
			if err := decodeConfigValue(raw, &cfg.AutoDerive); err != nil {
				writeAPIError(w, badTaskConfig("auto_derive debe ser booleano."))
				return
			}
		case "auto_evaluate":
			if err := decodeConfigValue(raw, &cfg.AutoEvaluate); err != nil {
				writeAPIError(w, badTaskConfig("auto_evaluate debe ser booleano."))
				return
			}
		case "auto_promote":
			var enabled bool
			if err := decodeConfigValue(raw, &enabled); err != nil || enabled {
				writeAPIError(w, badTaskConfig("La promoción requiere aprobación humana explícita; auto_promote no está permitido."))
				return
			}
			cfg.AutoPromote = false
		case "poll_seconds":
			if err := decodeConfigValue(raw, &cfg.PollSeconds); err != nil || cfg.PollSeconds < 2 || cfg.PollSeconds > 3600 {
				writeAPIError(w, badTaskConfig("poll_seconds debe estar entre 2 y 3600."))
				return
			}
		case "task_idle_seconds":
			if err := decodeConfigValue(raw, &cfg.TaskIdleSeconds); err != nil || cfg.TaskIdleSeconds < 15 || cfg.TaskIdleSeconds > 86400 {
				writeAPIError(w, badTaskConfig("task_idle_seconds debe estar entre 15 y 86400."))
				return
			}
		case "max_input_chars":
			if err := decodeConfigValue(raw, &cfg.MaxInputChars); err != nil || cfg.MaxInputChars < 1000 || cfg.MaxInputChars > 500000 {
				writeAPIError(w, badTaskConfig("max_input_chars debe estar entre 1000 y 500000."))
				return
			}
		case "repetitions":
			if err := decodeConfigValue(raw, &cfg.Repetitions); err != nil || cfg.Repetitions < 1 || cfg.Repetitions > 10 {
				writeAPIError(w, badTaskConfig("repetitions debe estar entre 1 y 10."))
				return
			}
		case "max_calls":
			if err := decodeConfigValue(raw, &cfg.MaxCalls); err != nil || cfg.MaxCalls < 2 || cfg.MaxCalls > 200 {
				writeAPIError(w, badTaskConfig("max_calls debe estar entre 2 y 200."))
				return
			}
		case "experiment_seconds":
			if err := decodeConfigValue(raw, &cfg.ExperimentSeconds); err != nil || cfg.ExperimentSeconds < 30 || cfg.ExperimentSeconds > 28800 {
				writeAPIError(w, badTaskConfig("experiment_seconds debe estar entre 30 y 28800."))
				return
			}
		case "min_quality_gain":
			if err := decodeConfigValue(raw, &cfg.MinQualityGain); err != nil || cfg.MinQualityGain < 0.001 || cfg.MinQualityGain > 1 {
				writeAPIError(w, badTaskConfig("min_quality_gain debe estar entre 0.001 y 1."))
				return
			}
		case "max_time_ratio":
			if err := decodeConfigValue(raw, &cfg.MaxTimeRatio); err != nil || cfg.MaxTimeRatio < 1 || cfg.MaxTimeRatio > 10 {
				writeAPIError(w, badTaskConfig("max_time_ratio debe estar entre 1 y 10."))
				return
			}
		case "min_efficiency_gain":
			if err := decodeConfigValue(raw, &cfg.MinEfficiencyGain); err != nil || cfg.MinEfficiencyGain < 0.01 || cfg.MinEfficiencyGain > 0.9 {
				writeAPIError(w, badTaskConfig("min_efficiency_gain debe estar entre 0.01 y 0.9."))
				return
			}
		case "max_source_bytes":
			if err := decodeConfigValue(raw, &cfg.MaxSourceBytes); err != nil || cfg.MaxSourceBytes < 1000 || cfg.MaxSourceBytes > MaxSourceBytes {
				writeAPIError(w, badTaskConfig("max_source_bytes debe estar entre 1000 y 67108864 (64 MiB)."))
				return
			}
		case "max_files_per_scan":
			if err := decodeConfigValue(raw, &cfg.MaxFilesPerScan); err != nil || cfg.MaxFilesPerScan < 1 || cfg.MaxFilesPerScan > 1000 {
				writeAPIError(w, badTaskConfig("max_files_per_scan debe estar entre 1 y 1000."))
				return
			}
		case "max_scan_seconds":
			if err := decodeConfigValue(raw, &cfg.MaxScanSeconds); err != nil || cfg.MaxScanSeconds < 1 || cfg.MaxScanSeconds > 120 {
				writeAPIError(w, badTaskConfig("max_scan_seconds debe estar entre 1 y 120."))
				return
			}
		case "provider":
			cfg.Provider, err = mergeTaskProviderConfig(cfg.Provider, raw)
			if err != nil {
				writeAPIError(w, badTaskConfig(err.Error()))
				return
			}
		case "replay":
			cfg.Replay, err = mergeTaskProviderConfig(cfg.Replay, raw)
			if err != nil {
				writeAPIError(w, badTaskConfig(err.Error()))
				return
			}
		}
	}
	newProvider, _ := jsonText(cfg.Provider)
	providerChanged = oldProvider != newProvider
	now := nowSeconds()
	var autoSince any
	if !oldAuto && cfg.AutoTasks {
		autoSince = now
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE task_settings SET auto_tasks=?,auto_tasks_since=CASE WHEN ?=1 AND auto_tasks=0 THEN ? ELSE auto_tasks_since END,auto_derive=?,auto_evaluate=?,poll_seconds=?,task_idle_seconds=?,max_input_chars=?,repetitions=?,max_calls=?,experiment_seconds=?,min_quality_gain=?,max_time_ratio=?,min_efficiency_gain=?,max_source_bytes=?,max_files_per_scan=?,max_scan_seconds=?,updated=? WHERE singleton=1`,
		boolInt(cfg.AutoTasks), boolInt(cfg.AutoTasks), autoSince, boolInt(cfg.AutoDerive), boolInt(cfg.AutoEvaluate), cfg.PollSeconds, cfg.TaskIdleSeconds, cfg.MaxInputChars, cfg.Repetitions, cfg.MaxCalls, cfg.ExperimentSeconds, cfg.MinQualityGain, cfg.MaxTimeRatio, cfg.MinEfficiencyGain, cfg.MaxSourceBytes, cfg.MaxFilesPerScan, cfg.MaxScanSeconds, now); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	for role, provider := range map[string]TaskProviderConfig{"provider": cfg.Provider, "replay": cfg.Replay} {
		if _, changed := request.Config[role]; !changed {
			continue
		}
		command, err := jsonText(provider.Command)
		if err != nil {
			writeAPIError(w, internalError(err))
			return
		}
		if _, err := tx.ExecContext(r.Context(), `UPDATE task_provider_configs SET kind=?,command_json=?,model=?,timeout_seconds=?,real_inference=?,model_provider=?,reasoning=?,updated=? WHERE role=?`,
			provider.Kind, command, provider.Model, provider.Timeout, boolInt(provider.RealInference), provider.Provider, provider.Reasoning, now, role); err != nil {
			writeAPIError(w, internalError(err))
			return
		}
	}
	if err := appendAudit(r.Context(), tx, "task_settings", "global", "settings_changed", map[string]any{"fields": fields}); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if providerChanged || (oldAuto && !cfg.AutoTasks) {
		m.cancelTaskProposalJobs(r.Context(), providerChanged, oldAuto && !cfg.AutoTasks)
	}
	m.notifyTaskScheduler()

	cfg.AutoPromote = false
	writeJSON(w, http.StatusOK, taskConfigResponse(cfg, m.providerDiagnostics(cfg)))
}

func decodeConfigValue[T any](raw json.RawMessage, value *T) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("null is not allowed")
	}
	return json.Unmarshal(raw, value)
}

func mergeTaskProviderConfig(current TaskProviderConfig, raw json.RawMessage) (TaskProviderConfig, error) {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return current, errors.New("provider/replay debe ser un objeto")
	}
	allowed := map[string]bool{"kind": true, "command": true, "model": true, "timeout": true, "real_inference": true, "provider": true, "reasoning": true}
	for key := range fields {
		if !allowed[key] {
			return current, errors.New("campo de provider desconocido: " + key)
		}
	}
	for key, value := range fields {
		var err error
		switch key {
		case "kind":
			err = decodeConfigValue(value, &current.Kind)
		case "command":
			err = decodeConfigValue(value, &current.Command)
		case "model":
			err = decodeConfigValue(value, &current.Model)
		case "timeout":
			err = decodeConfigValue(value, &current.Timeout)
		case "real_inference":
			err = decodeConfigValue(value, &current.RealInference)
		case "provider":
			err = decodeConfigValue(value, &current.Provider)
		case "reasoning":
			err = decodeConfigValue(value, &current.Reasoning)
		}
		if err != nil {
			return current, errors.New("tipo inválido en provider." + key)
		}
	}
	// Selecting OMP selects the engine this installation resolves: the stored
	// command can never name another program, so it is normalized, not rejected.
	// A reasoning level the operator never set for OMP is not inherited from
	// Pi's default either: OMP keeps its own native thinking until one is set.
	if current.Kind == "omp" {
		current.Command = []string{"omp"}
		if _, kindSent := fields["kind"]; kindSent {
			if _, sent := fields["reasoning"]; !sent {
				current.Reasoning = ""
			}
		}
	}
	return current, validateTaskProviderConfig(current, true)
}

func validateTaskProviderConfig(cfg TaskProviderConfig, allowUnconfigured bool) error {
	if cfg.Kind != "pi" && cfg.Kind != "codex" && cfg.Kind != "omp" && cfg.Kind != "command" {
		return errors.New("kind debe ser pi, codex, omp o command")
	}
	if len(cfg.Command) == 0 || len(cfg.Command) > 32 {
		return errors.New("command debe ser un array de argumentos no vacío y acotado")
	}
	for _, part := range cfg.Command {
		if part == "" || len(part) > 1024 || !utf8.ValidString(part) || strings.ContainsAny(part, "\x00\r\n") {
			return errors.New("command contiene un argumento inválido")
		}
	}
	if cfg.Kind != "command" && len(cfg.Command) != 1 {
		return errors.New("pi, codex y omp requieren un único ejecutable; no uses una shell")
	}
	// OMP's executable is the engine this installation resolves, never an argv the
	// stored configuration could point at another program.
	if cfg.Kind == "omp" && cfg.Command[0] != "omp" {
		return errors.New("command debe ser [\"omp\"]: el motor OMP resuelve su ejecutable instalado")
	}
	if cfg.Timeout < 5 || cfg.Timeout > 1800 {
		return errors.New("timeout debe estar entre 5 y 1800 segundos")
	}
	if len(cfg.Model) > 2000 || !utf8.ValidString(cfg.Model) || strings.ContainsAny(cfg.Model, "\x00\r\n") {
		return errors.New("model inválido")
	}
	if len(cfg.Provider) > 200 || !utf8.ValidString(cfg.Provider) || strings.ContainsAny(cfg.Provider, "\x00\r\n") {
		return errors.New("provider inválido")
	}
	if cfg.Kind == "pi" && cfg.Reasoning != "" && !validPiReasoning(cfg.Reasoning) {
		return errors.New("reasoning inválido")
	}
	if cfg.Kind == "omp" && cfg.Reasoning != "" && !validOMPReasoning(cfg.Reasoning) {
		return errors.New("reasoning inválido")
	}
	if cfg.Kind == "pi" && !allowUnconfigured && strings.TrimSpace(cfg.Model) == "" {
		return errors.New("Pi requiere un modelo explícito para inferencia")
	}
	return nil
}

func validPiReasoning(value string) bool {
	switch value {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

// validOMPReasoning adds OMP's own auto level to the levels Pi accepts.
func validOMPReasoning(value string) bool {
	return value == "auto" || validPiReasoning(value)
}

func (m *Module) providerDiagnostics(cfg TaskConfig) TaskConfigDiagnostics {
	provider := providerDiagnostic(m.agents, cfg.Provider, "provider")
	replay := providerDiagnostic(m.agents, cfg.Replay, "replay")
	ready := provider.Available && replay.Available
	reason := provider.Reason
	if reason == "" {
		reason = replay.Reason
	}
	return TaskConfigDiagnostics{Provider: provider, Replay: replay, Ready: ready, Reason: reason}
}

func providerDiagnostic(engines *harness.Engines, cfg TaskProviderConfig, role string) ProviderDiagnostics {
	binary := ""
	if len(cfg.Command) > 0 {
		if cfg.Kind == "codex" && cfg.Command[0] == "codex" || cfg.Kind == "omp" && cfg.Command[0] == "omp" {
			binary = engines.Resolve(cfg.Kind)
		} else {
			binary, _ = exec.LookPath(cfg.Command[0])
		}
	}
	available := binary != ""
	reason := ""
	if !available {
		reason = "No se encuentra el ejecutable configurado."
	}
	if cfg.Kind == "pi" && strings.TrimSpace(cfg.Model) == "" {
		available = false
		reason = "Pi requiere un modelo explícito antes de ejecutar inferencia."
	}
	if cfg.Kind == "codex" && role == "provider" {
		available = false
		reason = "Análisis Codex deshabilitado: no está verificada una opción del CLI que deshabilite todas las herramientas."
	}
	// OMP reads this host's Magpie configuration: only a routed model id names one
	// of its models, and an empty model keeps the native default of that host.
	if cfg.Kind == "omp" && strings.TrimSpace(cfg.Model) != "" && !harness.RoutedModel(cfg.Model) {
		available = false
		reason = "El motor OMP sólo ejecuta modelos de la Magpie de este host: usa un id magpie/<modelo> o deja el modelo vacío para el predeterminado nativo."
	}
	// An engine that is installed is not yet a route: only this host's Magpie can
	// answer for its own wiring, and it answers from its own state, read-only.
	if cfg.Kind == "omp" && role == "provider" && available {
		if err := engines.Probe(harness.EngineOMP, harness.OMPProfileDefault); err != nil {
			available = false
			reason = "La Magpie de este host no puede lanzar OMP: " + err.Error()
		}
	}
	if cfg.Kind == "omp" && role != "provider" {
		available = false
		reason = "OMP está habilitado sólo como proveedor de análisis en esta configuración."
	}
	return ProviderDiagnostics{Kind: cfg.Kind, Command: append([]string(nil), cfg.Command...), Model: cfg.Model, Provider: cfg.Provider,
		Reasoning: cfg.Reasoning, Timeout: cfg.Timeout, Available: available, Reason: reason}
}

func taskConfigResponse(cfg TaskConfig, diagnostics TaskConfigDiagnostics) TaskConfigResponse {
	return TaskConfigResponse{Config: cfg, Diagnostics: diagnostics}
}

func badTaskConfig(message string) *APIError {
	return &APIError{Status: http.StatusBadRequest, Code: "invalid_config", Message: message}
}

func (m *Module) cancelTaskProposalJobs(ctx context.Context, providerChanged, automaticDisabled bool) {
	query := `SELECT DISTINCT j.id FROM jobs j JOIN task_proposal_receipts r ON r.job_id=j.id WHERE j.kind='propose_tasks' AND j.status IN ('queued','running') AND ((?=1) OR (?=1 AND r.automatic=1))`
	m.cancelJobs(jobIDs(ctx, m.db, query, boolInt(providerChanged), boolInt(automaticDisabled))...)
}

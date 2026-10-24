// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workbench/internal/harness"
)

// ompStub writes one line per invocation (cwd and flattened argv, since the
// system prompt carries newlines), then prints the JSONL a tool-free OMP print
// session really prints.
const ompStub = `#!/bin/sh
printf 'CWD %s\nARGS %s\n' "$PWD" "$(printf '%s' "$*" | tr '\n' ' ')" >> "$OMP_STUB_LOG"
cat <<'JSONL'
{"type":"session","version":3,"id":"stub-session"}
{"type":"agent_start"}
{"type":"turn_start"}
{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"hi"}],"attribution":"user"}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"{\"observations\":[]}"}],"api":"openai-completions","provider":"stub-provider","model":"stub-model","usage":{"input":11,"output":7,"cacheRead":0,"cacheWrite":0,"totalTokens":18},"stopReason":"stop"}}
{"type":"turn_end","message":{"role":"assistant","stopReason":"stop"},"toolResults":[]}
{"type":"agent_end","messages":[],"isTerminal":true,"yielded":true}
JSONL
`

type ompTestRig struct {
	module *Module
	log    string
	engine *harness.Engines
}

// newOMPTestRig builds a Foundry whose OMP engine is a stub binary, so the argv,
// the working directory and the receipt are all observable without inference.
func newOMPTestRig(t *testing.T, router harness.Router) *ompTestRig {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(bin, "omp")
	if err := os.WriteFile(stub, []byte(ompStub), 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "argv.log")
	if router == nil {
		router = stubOMPRouter{}
	}
	engines, err := harness.New(harness.Options{StateDir: state, Router: router,
		Env: []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + filepath.Join(root, "user"), "OMP_STUB_LOG=" + log}})
	if err != nil {
		t.Fatal(err)
	}
	module, err := New(Options{StateDir: state, AgentEngines: engines})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close(context.Background()) })
	return &ompTestRig{module: module, log: log, engine: engines}
}

func (r *ompTestRig) argv(t *testing.T) (string, string) {
	t.Helper()
	raw, err := os.ReadFile(r.log)
	if err != nil {
		t.Fatalf("el motor OMP no se ejecutó: %v", err)
	}
	cwd, args := "", ""
	invocations := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		switch {
		case strings.HasPrefix(line, "CWD "):
			cwd, invocations = strings.TrimPrefix(line, "CWD "), invocations+1
		case strings.HasPrefix(line, "ARGS "):
			args = strings.TrimPrefix(line, "ARGS ")
		}
	}
	if invocations != 1 {
		t.Fatalf("el stub registró %d ejecuciones, se esperaba una", invocations)
	}
	return cwd, args
}

type stubOMPRouter struct{ err error }

func (s stubOMPRouter) AgentEnv(string, string) ([]string, error) { return nil, s.err }
func (s stubOMPRouter) CodexArgs(string) ([]string, error)        { return nil, nil }
func (s stubOMPRouter) Probe(string, string) error                { return s.err }

func ompAnalysisConfig() TaskProviderConfig {
	return TaskProviderConfig{Kind: "omp", Command: []string{"omp"}, Timeout: 30}
}

// The analysis must run with every tool surface off and with the model the host's
// Magpie owns: an empty model stays empty so OMP keeps its native default.
func TestOMPAnalysisRunsToolFreeWithNativeDefaultModel(t *testing.T) {
	rig := newOMPTestRig(t, nil)
	workspace := t.TempDir()
	run, err := rig.module.runTaskProvider(context.Background(), ompAnalysisConfig(), "inert evidence", taskObservationSchema, workspace, "analysis")
	if err != nil {
		t.Fatalf("análisis OMP con modelo nativo: %v", err)
	}
	if run.Text != `{"observations":[]}` || run.Provider != "omp" || run.Model != "stub-model" || run.ModelProvider != "stub-provider" {
		t.Fatalf("recibo OMP inesperado: %+v", run)
	}
	if run.ToolCalls == nil || *run.ToolCalls != 0 || run.Turns == nil || *run.Turns != 1 {
		t.Fatalf("recibo OMP sin turnos/herramientas honestos: %+v", run)
	}
	cwd, argv := rig.argv(t)
	for _, want := range []string{"--print", "--mode json", "--no-session", "--no-tools", "--no-extensions", "--no-lsp", "--no-skills", "--no-rules", "--system-prompt", "--session-dir"} {
		if !strings.Contains(argv, want) {
			t.Fatalf("argv OMP %q no declara %q", argv, want)
		}
	}
	// An allowlist beside --no-tools wins inside OMP, so it must never be there.
	if strings.Contains(argv, "--tools ") || strings.HasSuffix(argv, "--tools") {
		t.Fatalf("argv OMP pide herramientas: %q", argv)
	}
	if strings.Contains(argv, "--model") {
		t.Fatalf("un modelo vacío no debe reescribirse: %q", argv)
	}
	if strings.Contains(argv, "--thinking") {
		t.Fatalf("un razonamiento vacío no debe reescribirse: %q", argv)
	}
	// The run's own working directory is the empty analysis directory, so no
	// project context, plugin or MCP file is reachable from it.
	if filepath.Dir(filepath.Clean(cwd)) != filepath.Clean(workspace) || filepath.Base(cwd) != "analysis" {
		t.Fatalf("cwd del análisis OMP = %q, se esperaba %q", cwd, filepath.Join(workspace, "analysis"))
	}
	entries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("el análisis no corre en un directorio vacío: %v", entries)
	}

	// An explicit engine choice travels as the run's own flags; nothing else is
	// rewritten because the host's Magpie configuration owns the rest.
	t.Run("explicit model and thinking", func(t *testing.T) {
		rig := newOMPTestRig(t, nil)
		cfg := ompAnalysisConfig()
		cfg.Model, cfg.Reasoning = "magpie/codex/gpt-6-luna", "auto"
		if _, err := rig.module.runTaskProvider(context.Background(), cfg, "evidence", taskObservationSchema, t.TempDir(), "analysis"); err != nil {
			t.Fatalf("análisis OMP enrutado: %v", err)
		}
		if _, argv := rig.argv(t); !strings.Contains(argv, "--model magpie/codex/gpt-6-luna") || !strings.Contains(argv, "--thinking auto") {
			t.Fatalf("argv OMP enrutado = %q", argv)
		}
	})
}

// The analysis only runs through this host's Magpie configuration: a native model
// id, a missing routing seam and a host without an active route all stop it.
func TestOMPAnalysisRequiresHostMagpieRouting(t *testing.T) {
	rig := newOMPTestRig(t, nil)
	cfg := ompAnalysisConfig()
	cfg.Model = "gpt-6-luna"
	if _, err := rig.module.runTaskProvider(context.Background(), cfg, "evidence", taskObservationSchema, t.TempDir(), "analysis"); err == nil || !strings.Contains(err.Error(), "Magpie") {
		t.Fatalf("un modelo sin enrutar debe fallar con el motivo nativo, err=%v", err)
	}
	if _, err := os.Stat(rig.log); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("un modelo sin enrutar no debe llegar a ejecutar el motor")
	}

	without := newOMPTestRig(t, nil)
	without.engine = nil
	missing, err := harness.New(harness.Options{StateDir: filepath.Dir(without.log), Env: []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "OMP_STUB_LOG=" + without.log}})
	if err != nil {
		t.Fatal(err)
	}
	module, err := New(Options{StateDir: filepath.Dir(without.log), AgentEngines: missing})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close(context.Background()) })
	if _, err := module.runTaskProvider(context.Background(), ompAnalysisConfig(), "evidence", taskObservationSchema, t.TempDir(), "analysis"); err == nil || !strings.Contains(err.Error(), "Magpie") {
		t.Fatalf("sin la configuración Magpie del host el análisis debe fallar, err=%v", err)
	}

	// A host whose Magpie has no active route refuses before any process starts.
	offline := newOMPTestRig(t, stubOMPRouter{err: errors.New("this host's OMP profile is not connected to an active Magpie Codex route")})
	if _, err := offline.module.runTaskProvider(context.Background(), ompAnalysisConfig(), "evidence", taskObservationSchema, t.TempDir(), "analysis"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("un host sin ruta Magpie debe fallar con su propio motivo, err=%v", err)
	}
	if _, err := os.Stat(offline.log); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("un host sin ruta Magpie no debe ejecutar el motor")
	}
}

// OMP has no switch for the MCP servers it discovers and adds their tools even
// under --no-tools, so an analysis that cannot be tool-free is refused. Each path
// is one the installed build registers an MCP provider for.
func TestOMPAnalysisRefusesMCPConfiguredProfile(t *testing.T) {
	rig := newOMPTestRig(t, nil)
	home := rig.engine.AgentHome()
	agentDir := rig.engine.AgentDir(harness.OMPProfileDefault)
	project := t.TempDir()
	cases := []struct {
		name string
		path string
		body string
	}{
		{"OMP user file", filepath.Join(agentDir, "mcp.json"), `{"mcpServers":{"probe":{"command":"true"}}}`},
		{"Claude Code user file", filepath.Join(home, ".claude.json"), `{"mcpServers":{"probe":{"command":"true"}}}`},
		{"Claude Code user dir", filepath.Join(home, ".claude", "mcp.json"), `{"mcpServers":{"probe":{"command":"true"}}}`},
		{"Codex user config", filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.probe]\ncommand = \"true\"\n"},
		{"OpenCode user config", filepath.Join(home, ".config", "opencode", "opencode.json"), `{"mcp":{"probe":{"type":"local","command":["true"]}}}`},
		{"Cursor user config", filepath.Join(home, ".cursor", "mcp.json"), `{"mcpServers":{"probe":{"command":"true"}}}`},
		{"Windsurf user config", filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), `{"mcpServers":{"probe":{"command":"true"}}}`},
		{"Gemini user config", filepath.Join(home, ".gemini", "settings.json"), `{"mcpServers":{"probe":{"command":"true"}}}`},
	}
	// The analysis runs in the run's own empty directory, so a project file is only
	// reachable from there: the case plants it in that directory.
	projectCases := []struct {
		name string
		rel  string
		body string
	}{
		{"standalone project file", ".mcp.json", `{"mcpServers":{"probe":{"command":"true"}}}`},
		{"native project file", filepath.Join(".omp", "mcp.json"), `{"mcpServers":{"probe":{"command":"true"}}}`},
		{"VS Code project file", filepath.Join(".vscode", "mcp.json"), `{"servers":{"probe":{"command":"true"}}}`},
		{"OpenCode project file", "opencode.json", `{"mcp":{"probe":{"type":"local","command":["true"]}}}`},
	}
	run := func(t *testing.T, workspace, path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)
		_, err := rig.module.runTaskProvider(context.Background(), ompAnalysisConfig(), "evidence", taskObservationSchema, workspace, "analysis")
		if err == nil || !strings.Contains(err.Error(), "cero herramientas") || !strings.Contains(err.Error(), path) {
			t.Fatalf("un perfil con MCP en %s debe rechazar el análisis nombrando la ruta, err=%v", path, err)
		}
		if _, statErr := os.Stat(rig.log); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatal("el motor no debe ejecutarse cuando el análisis no puede ser sin herramientas")
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { run(t, t.TempDir(), tc.path, tc.body) })
	}
	for _, tc := range projectCases {
		t.Run(tc.name, func(t *testing.T) {
			run(t, project, filepath.Join(project, "analysis", tc.rel), tc.body)
		})
	}
}

// The diagnostics publish this host's own Magpie answer for its route, read-only:
// an installed engine with no route is not available, and a wired one is.
func TestOMPDiagnosticsFollowThisHostsRoute(t *testing.T) {
	ready := newOMPTestRig(t, nil)
	diagnostic := providerDiagnostic(ready.engine, ompAnalysisConfig(), "provider")
	if !diagnostic.Available || diagnostic.Reason != "" {
		t.Fatalf("una ruta Magpie preparada queda disponible sin motivo: %+v", diagnostic)
	}
	offline := newOMPTestRig(t, stubOMPRouter{err: errors.New("this host's OMP profile is not connected to an active Magpie Codex route")})
	diagnostic = providerDiagnostic(offline.engine, ompAnalysisConfig(), "provider")
	if diagnostic.Available || !strings.Contains(diagnostic.Reason, "not connected") || !strings.Contains(diagnostic.Reason, "Magpie") {
		t.Fatalf("sin ruta Magpie el motor no puede presentarse disponible: %+v", diagnostic)
	}
	// An unscoped model is named as such even before the route is asked for.
	unscoped := ompAnalysisConfig()
	unscoped.Model = "gpt-6-luna"
	diagnostic = providerDiagnostic(ready.engine, unscoped, "provider")
	if diagnostic.Available || !strings.Contains(diagnostic.Reason, "magpie/<modelo>") {
		t.Fatalf("un modelo sin enrutar no puede presentarse disponible: %+v", diagnostic)
	}
}

// OMP is enabled for the analysis provider only: its replay has no verified form.
func TestOMPReplayStaysDisabled(t *testing.T) {
	rig := newOMPTestRig(t, nil)
	if _, err := rig.module.runTaskProvider(context.Background(), ompAnalysisConfig(), "prompt", nil, t.TempDir(), "replay"); err == nil || !strings.Contains(err.Error(), "replay") {
		t.Fatalf("el replay OMP debe seguir deshabilitado, err=%v", err)
	}
	diagnostic := providerDiagnostic(rig.engine, ompAnalysisConfig(), "replay")
	if diagnostic.Available || !strings.Contains(diagnostic.Reason, "proveedor de análisis") {
		t.Fatalf("diagnóstico de replay OMP: %+v", diagnostic)
	}
}

// The OMP print session is parsed only with its own terminal event, and a
// response that used a tool or stopped early is never accepted as complete.
func TestOMPStreamParsingFailsClosed(t *testing.T) {
	message := map[string]any{"role": "assistant", "stopReason": "stop", "provider": "stub", "model": "m", "content": []any{map[string]any{"type": "text", "text": `{"observations":[]}`}}, "usage": map[string]any{"input": 3, "output": 2, "totalTokens": 5}}
	parsed, err := parseOMPResponse(providerJSONL(
		map[string]any{"type": "message_end", "message": message},
		map[string]any{"type": "turn_end"},
		map[string]any{"type": "agent_end", "isTerminal": true},
	), taskObservationSchema)
	if err != nil || parsed.text != `{"observations":[]}` || parsed.model != "m" || parsed.turns == nil || *parsed.turns != 1 {
		t.Fatalf("flujo OMP válido rechazado: %+v %v", parsed, err)
	}
	withoutEnd := providerJSONL(map[string]any{"type": "message_end", "message": message})
	if _, err := parseOMPResponse(withoutEnd, taskObservationSchema); err == nil || !strings.Contains(err.Error(), "OMP") {
		t.Fatal("un flujo OMP sin cierre debe rechazarse")
	}
	usedTool := providerJSONL(
		map[string]any{"type": "message_end", "message": message},
		map[string]any{"type": "tool_execution_start"},
		map[string]any{"type": "agent_end", "isTerminal": true},
	)
	if _, err := parseOMPResponse(usedTool, taskObservationSchema); err == nil {
		t.Fatal("un flujo OMP que ejecutó una herramienta debe rechazarse")
	}
	aborted := map[string]any{"role": "assistant", "stopReason": "aborted", "provider": "stub", "model": "m", "content": []any{map[string]any{"type": "text", "text": `{"observations":[]}`}}}
	if _, err := parseOMPResponse(providerJSONL(map[string]any{"type": "message_end", "message": aborted}, map[string]any{"type": "agent_end", "isTerminal": true}), taskObservationSchema); err == nil {
		t.Fatal("una respuesta OMP abortada debe rechazarse")
	}
}

// The UI reaches OMP through the same contract: kind omp is stored with this
// installation's engine name, an unscoped model fails closed, and the shared
// Codex/Pi guards stay exactly as they were.
func TestTaskConfigAcceptsOMPEngine(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "omp"), []byte(ompStub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	module, server := newFoundryTestServer(t, t.TempDir(), nil)
	defer server.Close()
	defer func() { _ = module.Close(context.Background()) }()

	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/tasks/config",
		map[string]any{"config": map[string]any{"provider": map[string]any{"kind": "omp"}}})
	if status != http.StatusOK {
		t.Fatalf("PATCH kind omp status=%d body=%+v", status, body)
	}
	provider := foundryObject(t, foundryObject(t, body["config"])["provider"])
	if provider["kind"] != "omp" || !equalJSON(provider["command"], []any{"omp"}) {
		t.Fatalf("el motor OMP no se guarda con su ejecutable: %+v", provider)
	}
	diagnostic := foundryObject(t, foundryObject(t, body["diagnostics"])["provider"])
	if diagnostic["model"] != "" {
		t.Fatalf("un modelo vacío es el predeterminado nativo: %+v", diagnostic)
	}
	// This module carries no routing seam at all, so the engine is installed but no
	// host route can answer for it: that limit is published, never a false green.
	// A wired host is the positive case of TestOMPDiagnosticsFollowThisHostsRoute.
	if diagnostic["available"] != false {
		t.Fatalf("sin la Magpie del host el motor no puede presentarse disponible: %+v", diagnostic)
	}
	if reason, _ := diagnostic["reason"].(string); !strings.Contains(reason, "Magpie") {
		t.Fatalf("el motivo de la ruta ausente es el de la instalación: %+v", diagnostic)
	}
	if reasoning, _ := diagnostic["reasoning"].(string); reasoning != "" {
		t.Fatalf("el razonamiento de OMP es nativo hasta que se pida otro: %+v", diagnostic)
	}

	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/tasks/config",
		map[string]any{"config": map[string]any{"provider": map[string]any{"kind": "omp", "command": []string{"pi"}}}})
	if status != http.StatusOK {
		t.Fatalf("PATCH un comando ajeno con kind omp status=%d body=%+v", status, body)
	}
	provider = foundryObject(t, foundryObject(t, body["config"])["provider"])
	if !equalJSON(provider["command"], []any{"omp"}) {
		t.Fatalf("el comando de OMP no puede nombrar otro programa: %+v", provider)
	}

	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/tasks/config",
		map[string]any{"config": map[string]any{"provider": map[string]any{"model": "gpt-6-luna"}}})
	if status != http.StatusOK {
		t.Fatalf("PATCH modelo sin enrutar status=%d body=%+v", status, body)
	}
	diagnostic = foundryObject(t, foundryObject(t, body["diagnostics"])["provider"])
	if diagnostic["available"] != false || !strings.Contains(diagnostic["reason"].(string), "Magpie") {
		t.Fatalf("un modelo sin enrutar no puede presentarse disponible: %+v", diagnostic)
	}

	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/tasks/config",
		map[string]any{"config": map[string]any{"provider": map[string]any{"kind": "claude"}}})
	if status != http.StatusBadRequest {
		t.Fatalf("un motor desconocido sigue siendo 400, status=%d body=%+v", status, body)
	}
	foundryErrorCode(t, body, "invalid_config")

	// The stored configuration round-trips: reload reports the same engine.
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/tasks/config", nil)
	if status != http.StatusOK {
		t.Fatalf("GET task config status=%d body=%+v", status, body)
	}
	provider = foundryObject(t, foundryObject(t, body["config"])["provider"])
	if provider["kind"] != "omp" || provider["model"] != "gpt-6-luna" || !equalJSON(provider["command"], []any{"omp"}) {
		t.Fatalf("la configuración OMP no sobrevive al reload: %+v", provider)
	}
	encoded, err := json.Marshal(provider)
	if err != nil || strings.Contains(string(encoded), "auth") {
		t.Fatalf("el contrato no publica credenciales: %s %v", encoded, err)
	}
}

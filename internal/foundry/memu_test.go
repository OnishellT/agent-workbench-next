// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"workbench/internal/harness"
)

func TestMemuPrepareRequiresConsentAndExportsOnlyEnabledCapturedSources(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	home := filepath.Join(root, "home")
	secretPath := filepath.Join(home, ".codex", "sessions", "secret.jsonl")
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, secretPath, map[string]any{
		"type":    "response_item",
		"payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "home-history-secret"}}},
	})
	t.Setenv("HOME", home)
	configPath := filepath.Join(root, "memu.env")
	if err := os.WriteFile(configPath, []byte("MEMU_DB="+filepath.Join(root, "memu.sqlite3")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMU_CONFIG_ENV", configPath)
	t.Setenv("MEMU_DB", "")
	memuLog := filepath.Join(root, "memu.log")
	t.Setenv("MEMU_STUB_LOG", memuLog)
	memuBin := writeExec(t, filepath.Join(root, "bin", "memu-codex"), `#!/bin/sh
printf '%s\n' "$*" > "$MEMU_STUB_LOG"
printf 'prepared 0 session(s) -> 0 job(s) in %s\n' "$PWD/jobs"
`)
	t.Setenv("WORKBENCH_MEMU_BIN", memuBin)

	module, server := newFoundryTestServer(t, stateDir, nil)
	enabledSource := captureMemuFixture(t, module, server, filepath.Join(root, "enabled"), "memu-enabled", "/workspace/memu-enabled")
	disabledSource := captureMemuFixture(t, module, server, filepath.Join(root, "disabled"), "memu-disabled", "/workspace/memu-disabled")
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/conversations/sources/"+disabledSource, map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("disable source: %d %+v", status, body)
	}

	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/foundry/memu/prepare", map[string]any{})
	if status != http.StatusConflict {
		t.Fatalf("prepare without consent: %d %+v", status, body)
	}
	foundryErrorCode(t, body, "memu_disabled")
	if matches, _ := filepath.Glob(filepath.Join(stateDir, "memu", "export", "*.jsonl")); len(matches) != 0 {
		t.Fatalf("disabled memU exported %v", matches)
	}

	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPut, "/api/foundry/memu", map[string]any{
		"enabled": true, "engine": "codex", "profile": "",
	})
	if status != http.StatusOK || body["enabled"] != true || body["engine"] != "codex" {
		t.Fatalf("enable memU: %d %+v", status, body)
	}
	exportDir := filepath.Join(stateDir, "memu", "export")
	if err := os.MkdirAll(exportDir, 0o700); err != nil {
		t.Fatal(err)
	}
	staleLink := filepath.Join(exportDir, "stale.jsonl")
	if err := os.Symlink(secretPath, staleLink); err != nil {
		t.Fatal(err)
	}

	module.captureMu.Lock()
	if _, err := module.db.Exec(`INSERT INTO sources(id,path,adapter,enabled,created,coverage_json) VALUES('uncaptured-memu',?, 'codex',1,?,?)`,
		filepath.Join(root, "uncaptured.jsonl"), nowSeconds(), `{"status":"unknown"}`); err != nil {
		module.captureMu.Unlock()
		t.Fatal(err)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/foundry/memu/prepare", map[string]any{})
	module.captureMu.Unlock()
	if status != http.StatusOK {
		t.Fatalf("prepare memU: %d %+v", status, body)
	}
	if body["configured"] != true || body["installed"] != true || body["phase"] != "idle" {
		t.Fatalf("memU state after prepare = %+v", body)
	}

	matches, err := filepath.Glob(filepath.Join(exportDir, "*.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("exported session files = %v, err=%v", matches, err)
	}
	if _, err := os.Lstat(staleLink); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale export symlink was not removed: %v", err)
	}
	var manifest memuExportManifest
	data, err := os.ReadFile(filepath.Join(exportDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Sessions) != 1 {
		t.Fatalf("manifest sessions = %+v", manifest.Sessions)
	}
	session := manifest.Sessions[0]
	if session.ID != "memu-enabled" || session.Host != "local" || session.Project != "/workspace/memu-enabled" || len(session.Citations) < 2 {
		t.Fatalf("manifest provenance = %+v", session)
	}
	if session.Citations[0].EventIndex != 0 || session.Citations[0].Kind != "session_metadata" || session.Citations[0].Exported {
		t.Fatalf("manifest metadata citation = %+v", session.Citations[0])
	}
	if !memuCitationExported(session.Citations, 1) {
		t.Fatalf("manifest exported citation = %+v", session.Citations)
	}
	if sourceID := sourceIDForSession(t, module, "memu-enabled"); sourceID != enabledSource {
		t.Fatalf("enabled source id = %s, want %s", sourceID, enabledSource)
	}

	exportBytes, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"home-history-secret", "memu-disabled", "uncaptured-memu"} {
		if strings.Contains(string(exportBytes), forbidden) {
			t.Fatalf("export contains revoked or uncaptured source %q: %s", forbidden, exportBytes)
		}
	}
	if !strings.Contains(string(exportBytes), `"type":"function_call"`) && !strings.Contains(string(exportBytes), `"text":"question"`) {
		t.Fatalf("Codex JSONL missing captured turns: %s", exportBytes)
	}
	if err := filepath.WalkDir(filepath.Join(stateDir, "memu"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), "home-history-secret") {
			return errors.New("HOME history leaked into memU state")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(memuLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "prepare --base-dir "+filepath.Join(stateDir, "memu")+" --session-dir "+exportDir+" --max-jobs 10") {
		t.Fatalf("memU prepare args = %s", args)
	}
}

func TestMemuPrepareReportsMissingExternalConfiguration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("MEMU_CONFIG_ENV", filepath.Join(root, "missing.env"))
	t.Setenv("MEMU_DB", "")
	t.Setenv("MEMU_MEMORY_MODE", "")
	logPath := filepath.Join(root, "memu.log")
	t.Setenv("MEMU_STUB_LOG", logPath)
	t.Setenv("WORKBENCH_MEMU_BIN", writeExec(t, filepath.Join(root, "bin", "memu-codex"), `#!/bin/sh
printf '%s\n' "$*" > "$MEMU_STUB_LOG"
`))
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPut, "/api/foundry/memu", map[string]any{
		"enabled": true, "engine": "codex", "profile": "",
	})
	if status != http.StatusOK || body["configured"] != false {
		t.Fatalf("enable memU without config: %d %+v", status, body)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/foundry/memu/prepare", map[string]any{})
	if status != http.StatusConflict || body["configured"] != nil {
		t.Fatalf("prepare without config: %d %+v", status, body)
	}
	foundryErrorCode(t, body, "memu_config_missing")
	if message, _ := body["error"].(string); !strings.Contains(message, "MEMU_DB") {
		t.Fatalf("missing config error = %q", message)
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("memU CLI ran without configuration: %v", err)
	}
}

func TestMemuBinaryRequiresAnAbsoluteConfiguredPath(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	writeExec(t, filepath.Join(bin, "memu-codex"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("WORKBENCH_MEMU_BIN", "memu-codex")
	configPath := filepath.Join(root, "memu.env")
	if err := os.WriteFile(configPath, []byte("MEMU_DB="+filepath.Join(root, "store.sqlite3")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMU_CONFIG_ENV", configPath)
	_, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/foundry/memu", nil)
	if status != http.StatusOK || body["installed"] != false {
		t.Fatalf("relative binary state: %d %+v", status, body)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPut, "/api/foundry/memu", map[string]any{
		"enabled": true, "engine": "codex", "profile": "",
	})
	if status != http.StatusOK {
		t.Fatalf("enable memU: %d %+v", status, body)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/foundry/memu/prepare", map[string]any{})
	if status != http.StatusConflict {
		t.Fatalf("relative binary prepare: %d %+v", status, body)
	}
	foundryErrorCode(t, body, "memu_unavailable")
	if matches, _ := filepath.Glob(filepath.Join(root, "state", "memu", "export", "*.jsonl")); len(matches) != 0 {
		t.Fatalf("relative binary exported files: %v", matches)
	}
}

func TestMemuProcessUsesNativeAgentWithoutModelOrCommit(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	configPath := filepath.Join(root, "memu.env")
	if err := os.WriteFile(configPath, []byte("MEMU_DB="+filepath.Join(root, "store.sqlite3")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "login-home"))
	t.Setenv("MEMU_CONFIG_ENV", configPath)
	t.Setenv("MEMU_DB", "")
	memuLog := filepath.Join(root, "memu.log")
	t.Setenv("MEMU_STUB_LOG", memuLog)
	memuBin := writeExec(t, filepath.Join(root, "memu-bin", "memu-codex"), `#!/bin/sh
printf '%s\n' "$*" > "$MEMU_STUB_LOG"
base=
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--base-dir" ]; then shift; base="$1"; fi
  shift
done
mkdir -p "$base/jobs"
printf 'Write one proposal under memory/.\nNever invoke memu-codex commit.\n' > "$base/jobs/1.txt"
printf 'prepared 0 session(s) -> 1 job(s) in %s\n' "$base/jobs"
`)
	t.Setenv("WORKBENCH_MEMU_BIN", memuBin)

	bin := filepath.Join(root, "agent-bin")
	codexArgs := filepath.Join(root, "codex.args")
	codexCwd := filepath.Join(root, "codex.cwd")
	codexHome := filepath.Join(root, "codex.home")
	codexPrompt := filepath.Join(root, "codex.prompt")
	codex := writeExec(t, filepath.Join(bin, "codex"), `#!/bin/sh
printf '%s\n' "$*" > "$CODEX_STUB_ARGS"
printf 'run\n' >> "$CODEX_STUB_ARGS.calls"
printf '%s\n' "$PWD" > "$CODEX_STUB_CWD"
printf '%s\n' "$HOME" > "$CODEX_STUB_HOME"
cat > "$CODEX_STUB_PROMPT"
mkdir -p memory
printf '# Proposed memory\n\nOnly in STATE/memu.\n' > memory/proposed.md
`)
	router := &memuTestRouter{}
	nativeHome := filepath.Join(root, "native-home")
	if err := os.MkdirAll(nativeHome, 0o700); err != nil {
		t.Fatal(err)
	}
	engines, err := harness.New(harness.Options{
		StateDir: stateDir,
		Router:   router,
		Env: []string{
			"PATH=" + bin + ":/usr/bin:/bin",
			"HOME=" + nativeHome,
			"WORKBENCH_CODEX_BIN=" + codex,
			"CODEX_STUB_ARGS=" + codexArgs,
			"CODEX_STUB_CWD=" + codexCwd,
			"CODEX_STUB_HOME=" + codexHome,
			"CODEX_STUB_PROMPT=" + codexPrompt,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	module, err := New(Options{StateDir: stateDir, AgentEngines: engines})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(module.Handler())
	t.Cleanup(func() {
		server.Close()
		_ = module.Close(context.Background())
	})

	captureMemuFixture(t, module, server, filepath.Join(root, "evidence"), "memu-process-evidence", root)

	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPut, "/api/foundry/memu", map[string]any{
		"enabled": true, "engine": "codex", "profile": "",
	})
	if status != http.StatusOK {
		t.Fatalf("enable memU: %d %+v", status, body)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/foundry/memu/prepare", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("prepare memU: %d %+v", status, body)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/foundry/memu/process", map[string]any{})
	if status != http.StatusOK || body["phase"] != "ready" {
		t.Fatalf("process memU: %d %+v", status, body)
	}
	files := foundryList(t, body["files"])
	if len(files) != 1 {
		t.Fatalf("proposal files = %+v", files)
	}
	file := foundryObject(t, files[0])
	if file["kind"] != "memory" || !strings.Contains(file["content"].(string), "Only in STATE/memu") || file["path"] == "" {
		t.Fatalf("proposal file = %+v", file)
	}
	jobs := foundryList(t, body["jobs"])
	if len(jobs) != 1 || foundryObject(t, jobs[0])["status"] != "succeeded" {
		t.Fatalf("memU jobs = %+v", jobs)
	}

	args, err := os.ReadFile(codexArgs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(" "+string(args)+" ", " -m ") {
		t.Fatalf("native agent selected a model: %s", args)
	}
	if router.model != "" {
		t.Fatalf("router received model %q", router.model)
	}
	home, _ := os.ReadFile(codexHome)
	if strings.TrimSpace(string(home)) != nativeHome {
		t.Fatalf("native HOME = %q, want %q", home, nativeHome)
	}
	cwd, _ := os.ReadFile(codexCwd)
	if strings.TrimSpace(string(cwd)) != filepath.Join(stateDir, "memu") {
		t.Fatalf("agent cwd = %q", cwd)
	}
	prompt, _ := os.ReadFile(codexPrompt)
	if !strings.Contains(string(prompt), "untrusted evidence") || !strings.Contains(string(prompt), "Never invoke \"memu-codex commit\"") {
		t.Fatalf("process prompt = %s", prompt)
	}
	logged, _ := os.ReadFile(memuLog)
	if strings.Contains(string(logged), "commit") || !strings.Contains(string(logged), "prepare") {
		t.Fatalf("memU CLI invocation = %s", logged)
	}
	before, err := os.ReadFile(codexArgs + ".calls")
	if err != nil {
		t.Fatal(err)
	}
	status, _ = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/foundry/memu/process", map[string]any{})
	after, err := os.ReadFile(codexArgs + ".calls")
	if status != http.StatusOK || err != nil || string(after) != string(before) {
		t.Fatalf("processing completed jobs invoked the agent again: status=%d, %v", status, err)
	}
}

type memuTestRouter struct {
	mu    sync.Mutex
	model string
}

func (r *memuTestRouter) AgentEnv(string, string) ([]string, error) { return nil, nil }
func (r *memuTestRouter) CodexArgs(model string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.model = model
	return nil, nil
}
func (r *memuTestRouter) Probe(string, string) error { return nil }

func captureMemuFixture(t *testing.T, module *Module, server *httptest.Server, dir, sessionID, cwd string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(dir, sessionID+".jsonl"),
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": sessionID, "cwd": cwd}},
		map[string]any{"timestamp": "2026-10-18T10:00:00Z", "type": "response_item", "payload": map[string]any{
			"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "question " + sessionID}},
		}},
		map[string]any{"timestamp": "2026-10-18T10:00:01Z", "type": "response_item", "payload": map[string]any{
			"type": "function_call", "name": "shell", "arguments": `{"cmd":"pwd"}`, "call_id": "call-" + sessionID,
		}},
	)
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{
		"path": dir, "adapter": "codex",
	})
	if status != http.StatusCreated {
		t.Fatalf("create %s source: %d %+v", sessionID, status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)
	queueFoundryScan(t, server.Client(), server.URL, sourceID)
	var count int
	if err := module.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, sessionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("captured sessions for %s = %d", sessionID, count)
	}
	return sourceID
}

func sourceIDForSession(t *testing.T, module *Module, sessionID string) string {
	t.Helper()
	var sourceID string
	if err := module.db.QueryRow(`SELECT source_id FROM sessions WHERE id=?`, sessionID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	return sourceID
}

func memuCitationExported(citations []memuExportCitation, index int) bool {
	for _, citation := range citations {
		if citation.EventIndex == index {
			return citation.Exported
		}
	}
	return false
}

func writeExec(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMemuRevocationCancelsProcessingAndHidesPublishedArtifacts(t *testing.T) {
	root := t.TempDir()
	m, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	source := captureMemuFixture(t, m, server, filepath.Join(root, "evidence"), "memu-revoked-session", root)
	base, err := m.memuBase()
	if err != nil {
		t.Fatal(err)
	}
	if _, apiErr := m.exportMemuEvidence(context.Background(), filepath.Join(base, "export")); apiErr != nil {
		t.Fatal(apiErr)
	}
	settings, _, apiErr := m.loadMemuSettings()
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	settings.Published = true
	if apiErr := m.writeMemuSettings(base, settings); apiErr != nil {
		t.Fatal(apiErr)
	}
	if err := os.MkdirAll(filepath.Join(base, "skill"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "skill", "proposal.md"), []byte("# Test proposal"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.MemuEvidenceAuthorized(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := m.watchMemuEvidence(ctx, cancel, base)
	defer stop()
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/conversations/sources/"+source, map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("revoke = %d %+v", status, body)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("source revocation did not cancel processing")
	}
	if err := m.MemuEvidenceAuthorized(); err == nil {
		t.Fatal("revoked proposals authorized")
	}
	state, apiErr := m.memuState(context.Background(), settings)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if len(state.Files) != 0 {
		t.Fatal("revoked proposals still published")
	}
}

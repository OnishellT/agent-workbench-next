package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testEngines(t *testing.T, router Router) (*Engines, string, string) {
	if router == nil {
		router = stubRouter{}
	}
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "user-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	engines, err := New(Options{
		StateDir: filepath.Join(root, "state"),
		Env:      []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "OMP_AUTH_BROKER_TOKEN=leak"},
		Router:   router,
	})
	if err != nil {
		t.Fatal(err)
	}
	return engines, root, bin
}

func writeStub(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultEnvironmentResolvesInstalledEngine(t *testing.T) {
	root := t.TempDir()
	codex := writeStub(t, filepath.Join(root, "codex"))
	t.Setenv("PATH", root)
	t.Setenv("HOME", root)
	t.Setenv("WORKBENCH_CODEX_BIN", "")
	engines, err := New(Options{StateDir: filepath.Join(root, "state")})
	if err != nil {
		t.Fatal(err)
	}
	if got := engines.Resolve(EngineCodex); got != codex {
		t.Fatalf("default environment resolved %q, want %q", got, codex)
	}
	explicit, err := New(Options{StateDir: filepath.Join(root, "explicit"), Env: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if envMap(explicit.env)["PATH"] != "" {
		t.Fatal("an explicitly empty environment must not inherit PATH")
	}
}

func TestResolvePrefersConfiguredBinary(t *testing.T) {
	engines, _, bin := testEngines(t, nil)
	writeStub(t, filepath.Join(bin, "codex"))
	configured := writeStub(t, filepath.Join(bin, "my-codex"))
	engines.env = append(engines.env, "WORKBENCH_CODEX_BIN="+configured)
	got := engines.Resolve(EngineCodex)
	if got != configured {
		t.Fatalf("configured binary not used: got %q want %q", got, configured)
	}
	if engines.Resolve("claude") != "" || engines.Resolve("pi") != "" {
		t.Fatal("engines outside the legacy pair must not resolve")
	}
}

func TestResolveRejectsPrivateAliases(t *testing.T) {
	engines, root, bin := testEngines(t, nil)
	alias := filepath.Join(root, "state", "magpie", "bin", "omp")
	if err := os.MkdirAll(filepath.Dir(alias), 0o700); err != nil {
		t.Fatal(err)
	}
	writeStub(t, alias)
	// A PATH entry that only reaches the alias through a symlink is rejected too.
	link := filepath.Join(bin, "omp")
	if err := os.Symlink(alias, link); err != nil {
		t.Fatal(err)
	}
	if got := engines.Resolve(EngineOMP); got != "" {
		t.Fatalf("private alias accepted: %q", got)
	}
	direct := writeStub(t, filepath.Join(bin, "real-omp"))
	engines.env = append(engines.env, "WORKBENCH_OMP_BIN="+direct)
	if got := engines.Resolve(EngineOMP); got != direct {
		t.Fatalf("explicit binary rejected: %q", got)
	}
}

func TestCodexArgumentsAndModelOverride(t *testing.T) {
	engines, _, bin := testEngines(t, nil)
	codex := writeStub(t, filepath.Join(bin, "codex"))
	command, err := engines.Command(Spec{Engine: EngineCodex, Permission: PermissionReadOnly, NativeID: "thread-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "resume", "thread-1", "--json", "--skip-git-repo-check", "-c", `sandbox_mode="read-only"`, "-"}
	if command.Path != codex || strings.Join(command.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("codex argv = %q, want %q", command.Args, want)
	}
	for _, model := range []string{"gpt-6-luna", "fixture/other", "magpie/provider/model"} {
		command, err = engines.Command(Spec{Engine: EngineCodex, Permission: PermissionReadOnly, Model: model})
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(command.Args, " ")
		if !strings.HasSuffix(joined, `sandbox_mode="read-only" -m `+model+" -") || strings.Count(joined, "-m ") != 1 {
			t.Fatalf("native Codex model must stay intact and occur once: %q", command.Args)
		}
		if _, ok := envMap(command.Env)["ROUTED"]; ok {
			t.Fatal("native Codex configuration must not be replaced with generated provider environment")
		}
	}
	engines.router = nil
	for _, model := range []string{"", "fixture/model", "magpie/provider/model"} {
		if _, err := engines.Command(Spec{Engine: EngineCodex, Permission: PermissionReadOnly, Model: model}); err == nil {
			t.Fatalf("Codex model %q bypassed the native Magpie configuration", model)
		}
	}
	engines.router = stubRouter{err: os.ErrPermission}
	if _, err := engines.Command(Spec{Engine: EngineCodex, Permission: PermissionReadOnly}); err != os.ErrPermission {
		t.Fatalf("native Magpie refusal must prevent default Codex launch: %v", err)
	}
}

type stubRouter struct {
	args []string
	err  error
}

func (s stubRouter) AgentEnv(engine, profile string) ([]string, error) {
	return []string{"ROUTED=" + engine + "/" + profile}, nil
}

func (s stubRouter) CodexArgs(model string) ([]string, error) { return s.args, s.err }

func (s stubRouter) Probe(engine, profile string) error { return nil }

func TestOMPArgumentsPerProfileAndPermission(t *testing.T) {
	engines, state, bin := testEngines(t, nil)
	writeStub(t, filepath.Join(bin, "omp"))
	command, err := engines.Command(Spec{Engine: EngineOMP, Profile: "work", Permission: PermissionReadOnly, SessionDir: filepath.Join(state, "work", "s1", "omp")})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command.Args, " ")
	for _, want := range []string{"--print", "--mode json", "--no-title", "--no-pty", "--max-time 30m", "--tools read,grep,glob", "--no-extensions", "--no-lsp", "--approval-mode yolo"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("OMP argv %q misses %q", joined, want)
		}
	}
	if strings.Contains(joined, "--model") {
		t.Fatalf("empty model must keep the CLI default: %q", joined)
	}
	command, err = engines.Command(Spec{Engine: EngineOMP, Profile: "personal", Permission: PermissionWorkspaceWrite, Model: "magpie/codex/gpt-6-luna"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.Join(command.Args, " "), "--model magpie/codex/gpt-6-luna --approval-mode yolo") {
		t.Fatalf("OMP workspace-write argv = %q", command.Args)
	}
	command, err = engines.Command(Spec{Engine: EngineOMP, Permission: PermissionReadOnly, Model: "magpie/codex/gpt-6-luna", Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(command.Args, " ") != "--model magpie/codex/gpt-6-luna" {
		t.Fatalf("OMP PTY argv = %q", command.Args)
	}
	if len(command.Args) != 2 {
		t.Fatalf("OMP PTY must not add non-interactive flags: %q", command.Args)
	}
}

func TestCommandValidation(t *testing.T) {
	engines, _, bin := testEngines(t, nil)
	writeStub(t, filepath.Join(bin, "codex"))
	writeStub(t, filepath.Join(bin, "omp"))
	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{"engine", Spec{Engine: "claude", Permission: PermissionReadOnly}, "Motor inválido"},
		{"engine missing", Spec{Engine: "omp", Permission: PermissionReadOnly}, "Motor no instalado"},
		{"profile", Spec{Engine: EngineOMP, Profile: "otro", Permission: PermissionReadOnly}, "Perfil OMP inválido"},
		{"profile for codex", Spec{Engine: EngineCodex, Profile: "work", Permission: PermissionReadOnly}, "El perfil solo corresponde a OMP"},
		{"model length", Spec{Engine: EngineOMP, Permission: PermissionReadOnly, Model: strings.Repeat("m", 501)}, "Modelo inválido"},
		{"model control", Spec{Engine: EngineOMP, Permission: PermissionReadOnly, Model: "bad\nmodel"}, "Modelo inválido"},
		{"permission", Spec{Engine: EngineOMP, Permission: "sudo"}, "Permiso inválido"},
		{"library profile", Spec{Engine: EngineOMP, Profile: "work", Permission: PermissionReadOnly, Library: true}, "La biblioteca DFM se instala en el perfil principal"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			instance := engines
			if test.name == "engine missing" {
				missing, _, _ := testEngines(t, nil)
				missing.env = append(missing.env, "WORKBENCH_OMP_BIN="+filepath.Join(t.TempDir(), "missing"))
				instance = missing
			}
			_, err := instance.Command(test.spec)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLaunchEnvIsPrivateAndExplicit(t *testing.T) {
	engines, root, bin := testEngines(t, stubRouter{})
	writeStub(t, filepath.Join(bin, "omp"))
	command, err := engines.Command(Spec{Engine: EngineOMP, Profile: "personal", Permission: PermissionReadOnly, Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	env := envMap(command.Env)
	home := filepath.Join(root, "state", "agents", "home")
	if env["HOME"] != home || env["CODEX_HOME"] != filepath.Join(home, ".codex") {
		t.Fatalf("private homes missing: HOME=%q CODEX_HOME=%q", env["HOME"], env["CODEX_HOME"])
	}
	if info, err := os.Stat(env["CODEX_HOME"]); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("CODEX_HOME must exist as a private directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env["CODEX_HOME"], "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("the launcher must not create or import Codex credentials: %v", err)
	}
	for name, want := range map[string]string{
		"XDG_CONFIG_HOME":     filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":      filepath.Join(home, ".cache"),
		"XDG_DATA_HOME":       filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":      filepath.Join(home, ".local", "state"),
		"XDG_RUNTIME_DIR":     filepath.Join(root, "state", "agents", "run"),
		"PI_CONFIG_DIR":       ".omp",
		"PI_CODING_AGENT_DIR": filepath.Join(home, ".omp", "profiles", "personal", "agent"),
		"OMP_PROFILE":         "personal",
		"TERM":                "xterm-256color",
	} {
		if env[name] != want {
			t.Fatalf("%s = %q, want %q", name, env[name], want)
		}
	}
	if _, ok := env["OMP_AUTH_BROKER_TOKEN"]; ok {
		t.Fatal("legacy broker variables must not reach the engine")
	}
	if strings.Contains(env["PATH"], home) {
		t.Fatalf("discovery path must keep the user installs, not the private home: %q", env["PATH"])
	}
	if env["PI_CONFIG_FILES"] != "" || env["OMP_CONFIG_DIR"] != "" {
		t.Fatal("legacy OMP config overrides must be removed")
	}
}

func TestProfilePresetsArePrivateAndPinned(t *testing.T) {
	engines, root, bin := testEngines(t, nil)
	writeStub(t, filepath.Join(bin, "omp"))
	if _, err := engines.Command(Spec{Engine: EngineOMP, Profile: "personal", Permission: PermissionReadOnly}); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(root, "state", "agents", "home", ".omp", "profiles", "personal", "agent")
	for name, want := range map[string]string{
		"config.yml":        presetHashes["assets/personal/config.yml"],
		"extensions/rtk.ts": presetHashes["assets/personal/extensions/rtk.ts"],
	} {
		raw, err := os.ReadFile(filepath.Join(agent, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := sha256Hex(raw); got != want {
			t.Fatalf("%s = %s, want pinned %s", name, got, want)
		}
	}
	marker := filepath.Join(agent, ".workbench-profile.json")
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"profile": "personal"`) {
		t.Fatalf("marker = %s", raw)
	}
	if info, err := os.Stat(filepath.Join(agent, "config.yml")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("preset mode = %v, want 0600", info.Mode().Perm())
	}
	// The main profile keeps OMP's own files: no preset is written for it.
	if _, err := engines.Command(Spec{Engine: EngineOMP, Permission: PermissionReadOnly}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "agents", "home", ".omp", "agent", "config.yml")); !os.IsNotExist(err) {
		t.Fatalf("default profile must keep OMP's own configuration: %v", err)
	}
}

func TestProfileRejectsSymlinkedDirectory(t *testing.T) {
	engines, root, bin := testEngines(t, nil)
	writeStub(t, filepath.Join(bin, "omp"))
	home := filepath.Join(root, "state", "agents", "home")
	if err := os.MkdirAll(filepath.Join(home, ".omp", "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, ".omp", "profiles", "work")); err != nil {
		t.Fatal(err)
	}
	if _, err := engines.Command(Spec{Engine: EngineOMP, Profile: "work", Permission: PermissionReadOnly}); err == nil {
		t.Fatal("a symlinked profile directory must be rejected")
	}
}

func TestProfileFileGuards(t *testing.T) {
	engines, root, bin := testEngines(t, nil)
	writeStub(t, filepath.Join(bin, "omp"))
	agent := filepath.Join(root, "state", "agents", "home", ".omp", "profiles", "personal", "agent")
	spec := Spec{Engine: EngineOMP, Profile: "personal", Permission: PermissionReadOnly}

	// An existing user file is preserved exactly as it is.
	if err := os.MkdirAll(agent, 0o700); err != nil {
		t.Fatal(err)
	}
	custom := []byte("# perfil propio\n")
	if err := os.WriteFile(filepath.Join(agent, "config.yml"), custom, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := engines.Command(spec); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(agent, "config.yml")); err != nil || string(raw) != string(custom) {
		t.Fatalf("user preset was replaced: %q %v", raw, err)
	}

	// A preset path that is not a regular file is an error, not a replacement.
	guarded := filepath.Join(root, "state", "agents", "home", ".omp", "profiles", "work", "agent")
	if err := os.MkdirAll(filepath.Join(guarded, "config.yml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := engines.Command(Spec{Engine: EngineOMP, Profile: "work", Permission: PermissionReadOnly}); err == nil {
		t.Fatal("a directory in place of a preset must fail")
	}

	// A symlinked preset or marker is refused: ownership must not be guessed.
	link := filepath.Join(root, "state", "agents", "home", ".omp", "profiles", "personal", "agent", ".workbench-profile.json")
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "nowhere.json"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := engines.Command(spec); err == nil {
		t.Fatal("a symlinked profile marker must fail")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(link, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := engines.Command(spec); err == nil {
		t.Fatal("a directory in place of the profile marker must fail")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}

	// An unreadable marker is reported instead of being treated as empty.
	if _, err := engines.Command(spec); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if err := os.Chmod(link, 0o000); err != nil {
			t.Fatal(err)
		}
		if _, err := engines.Command(spec); err == nil {
			t.Fatal("an unreadable profile marker must fail")
		}
		if err := os.Chmod(link, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestBundledPluginAndLibraryAreExtractedAndLinked proves the single-binary
// path: the plugin tree and the DFM library come from the embedded bundle, are
// verified against their pins, extracted under the private state and linked
// through the OMP binary that the user has installed.
func TestBundledPluginAndLibraryAreExtractedAndLinked(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "links.txt")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + log + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "omp"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	engines, err := New(Options{StateDir: state, Env: []string{"HOME=" + root, "PATH=" + bin + ":/usr/bin:/bin"}, Router: stubRouter{}})
	if err != nil {
		t.Fatal(err)
	}
	// The main profile links nothing; a named profile links the plugin.
	if _, err := engines.Command(Spec{Engine: EngineOMP, Permission: PermissionReadOnly}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "agents", "omp", "bundle")); !os.IsNotExist(err) {
		t.Fatalf("the main profile must not extract the plugin bundle: %v", err)
	}
	if _, err := engines.Command(Spec{Engine: EngineOMP, Profile: "personal", Permission: PermissionReadOnly}); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(state, "agents", "omp", "bundle", bundleRevision(), "plugins", "ponytail")
	if _, err := os.Stat(filepath.Join(plugin, "plugin.yaml")); err != nil {
		t.Fatalf("plugin bundle not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plugin, "assets")); !os.IsNotExist(err) {
		t.Fatal("the documentation assets must not be embedded")
	}
	agent := filepath.Join(state, "agents", "home", ".omp", "profiles", "personal", "agent")
	raw, err := os.ReadFile(filepath.Join(agent, ".workbench-profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), plugin) {
		t.Fatalf("marker does not record the linked plugin: %s", raw)
	}
	links, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(links), "plugin\nlink\n"+plugin) {
		t.Fatalf("plugin link was not called: %q", links)
	}

	// The explicit DFM library installs into the main profile only.
	if _, err := engines.Command(Spec{Engine: EngineOMP, Permission: PermissionReadOnly, Library: true}); err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(state, "agents", "omp", "bundle", bundleRevision(), "skill-libraries", "dfm-project-kit")
	if _, err := os.Stat(filepath.Join(library, "package.json")); err != nil {
		t.Fatalf("skill library not extracted: %v", err)
	}
	links, err = os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(links), "plugin\nlink\n"+library) {
		t.Fatalf("library link was not called: %q", links)
	}
	mainMarker, err := os.ReadFile(filepath.Join(state, "agents", "home", ".omp", "agent", ".workbench-profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainMarker), `"dfm"`) {
		t.Fatalf("main profile marker = %s", mainMarker)
	}

	// A second run is idempotent: the files stay and the link is not repeated.
	before, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engines.Command(Spec{Engine: EngineOMP, Profile: "personal", Permission: PermissionReadOnly}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("the plugin was linked again: %q -> %q", before, after)
	}
}

// TestLaunchEnvReachesARealProcess runs a stub engine to prove the prepared
// environment is what the child actually sees.
func TestLaunchEnvReachesARealProcess(t *testing.T) {
	engines, root, bin := testEngines(t, nil)
	stub := filepath.Join(bin, "codex")
	script := "#!/bin/sh\nprintf '%s\\n' \"$HOME\" \"$CODEX_HOME\" \"$1\" > " + filepath.Join(root, "seen.txt") + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	command, err := engines.Command(Spec{Engine: EngineCodex, Permission: PermissionReadOnly, Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command(command.Path, append(command.Args, "marker")...)
	process.Env = command.Env
	if output, err := process.CombinedOutput(); err != nil {
		t.Fatalf("stub engine: %v: %s", err, output)
	}
	raw, err := os.ReadFile(filepath.Join(root, "seen.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	home := filepath.Join(root, "state", "agents", "home")
	if len(lines) != 3 || lines[0] != home || lines[1] != filepath.Join(home, ".codex") || lines[2] != "marker" {
		t.Fatalf("child saw %q", lines)
	}
}

// TestOMPToolFreeAnalysisFlags locks the only argv an analysis of untrusted
// evidence may run with, and the fact that no other engine can claim it.
func TestOMPToolFreeAnalysisFlags(t *testing.T) {
	engines, root, bin := testEngines(t, nil)
	writeStub(t, filepath.Join(bin, "omp"))
	command, err := engines.Command(Spec{Engine: EngineOMP, Permission: PermissionReadOnly, SessionDir: filepath.Join(root, "sessions"), NoTools: true})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command.Args, " ")
	for _, want := range []string{"--no-tools", "--no-extensions", "--no-lsp", "--no-skills", "--no-rules", "--no-pty", "--approval-mode yolo"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("tool-free OMP argv %q misses %q", joined, want)
		}
	}
	// An allowlist beside --no-tools turns the tools back on inside OMP.
	if strings.Contains(joined, "--tools") {
		t.Fatalf("tool-free OMP argv carries an allowlist: %q", joined)
	}
	if _, err := engines.Command(Spec{Engine: EngineCodex, Permission: PermissionReadOnly, NoTools: true}); err == nil {
		t.Fatal("Codex has no verified tool-free form and must be refused")
	}
	if _, err := engines.Command(Spec{Engine: EngineOMP, Permission: PermissionWorkspaceWrite, NoTools: true}); err != nil {
		t.Fatalf("a writing OMP run keeps its permission: %v", err)
	}
	if _, err := engines.Command(Spec{Engine: EngineOMP, Permission: PermissionReadOnly, NoTools: true, Interactive: true}); err == nil {
		t.Fatal("an interactive session cannot be tool-free")
	}
	want := filepath.Join(root, "state", "agents", "home", ".omp", "agent")
	if got := engines.AgentDir(OMPProfileDefault); got != want {
		t.Fatalf("AgentDir = %q, want %q", got, want)
	}
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

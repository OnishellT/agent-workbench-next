// Package harness launches the native agents installed on this machine (Codex
// and OMP) with explicit private homes. It is the Go port of the legacy
// agents.py resolution, the engine command builders and the OMP profile setup
// from apps/omp/profile_config.py. Nothing here reads a user's credentials.
package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Engine identifiers accepted by Work and Terminal, as in the legacy source.
const (
	EngineCodex = "codex"
	EngineOMP   = "omp"
)

// OMPProfileDefault keeps OMP's main profile; the named profiles own separate
// private agent directories and never inherit another profile's credentials.
const OMPProfileDefault = "default"

// OMP names the native gateway provider in its model selector.
const routedModelPrefix = "magpie/"

var ompProfiles = map[string]bool{"default": true, "personal": true, "work": true}

func RoutedModel(model string) bool { return strings.HasPrefix(model, routedModelPrefix) }

// Options configure the engine launcher.
type Options struct {
	// StateDir is the workbench state root; agent homes live under it.
	StateDir string
	// Env is the base environment; nil means the current process environment.
	Env []string
	// Router is owned by this host's native Magpie.
	Router Router
}

// Router reads this host's native Magpie state without configuring providers.
type Router interface {
	// AgentEnv returns environment entries the routing layer owns.
	AgentEnv(engine, profile string) ([]string, error)
	// CodexArgs returns the Codex arguments that route model through Magpie.
	CodexArgs(model string) ([]string, error)
	// Probe reports whether this host can launch engine right now, reading only
	// its own native state: no preparation, no connection, no drift repair and no
	// provider call. A diagnostics read publishes it.
	Probe(engine, profile string) error
}

type Engines struct {
	state  string
	env    []string
	router Router
}

// New validates the state root and prepares the launcher.
func New(opts Options) (*Engines, error) {
	if strings.TrimSpace(opts.StateDir) == "" || !filepath.IsAbs(opts.StateDir) {
		return nil, errors.New("el estado del lanzador requiere una ruta absoluta")
	}
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}
	return &Engines{state: filepath.Clean(opts.StateDir), env: append([]string(nil), env...), router: opts.Router}, nil
}

// Resolve returns the executable for engine, or "" when it is not installed.
// It keeps the discovered path spelling so installed wrappers keep argv[0].
func (e *Engines) Resolve(engine string) string {
	if engine != EngineCodex && engine != EngineOMP {
		return ""
	}
	env := envMap(e.env)
	path := nativePath(env)
	forbidden := map[string]bool{}
	// A private alias must never become the source of its own replacement.
	forbidden[absJoin(e.state, "magpie", "bin", engine)] = true
	if engine == EngineCodex {
		forbidden[absJoin(e.state, "workbench-bin", "codex-magpie")] = true
	}
	if configured := env["WORKBENCH_"+strings.ToUpper(engine)+"_BIN"]; configured != "" {
		name := configured
		if strings.HasPrefix(name, "~/") && env["HOME"] != "" {
			name = filepath.Join(env["HOME"], name[2:])
		}
		return usable(lookPath(name, path), forbidden)
	}
	for _, directory := range strings.Split(path, string(os.PathListSeparator)) {
		if directory == "" {
			directory = "."
		}
		if found := usable(lookPathIn(engine, directory), forbidden); found != "" {
			return found
		}
	}
	return ""
}

// AgentHome is the private home every engine launch is confined to.
func (e *Engines) AgentHome() string { return filepath.Join(e.state, "agents", "home") }

// Probe reports whether engine can run under profile on this host right now. It
// asks the routing layer's own state only and prepares nothing, so a diagnostic
// read can neither change configuration nor reach a provider.
func (e *Engines) Probe(engine, profile string) error {
	if engine != EngineOMP {
		return errors.New("La disponibilidad sólo se comprueba para OMP")
	}
	if e.router == nil {
		return errors.New("OMP requiere la configuración Magpie de este host")
	}
	if profile == "" {
		profile = OMPProfileDefault
	}
	if !ompProfiles[profile] {
		return errors.New("Perfil OMP inválido")
	}
	return e.router.Probe(engine, profile)
}

// AgentDir is the profile's own agent directory inside this launcher's private
// home: the tree OMP reads its own user-level configuration from.
func (e *Engines) AgentDir(profile string) string {
	if profile == "" {
		profile = OMPProfileDefault
	}
	return ompAgentDir(e.AgentHome(), profile)
}

// Spec describes one engine invocation.
type Spec struct {
	Engine      string
	Profile     string
	Model       string
	Permission  string
	NativeID    string
	SessionDir  string
	Library     bool
	Interactive bool
	// NoTools runs the one-shot OMP print session with every built-in tool off.
	// It is the only form used for the analysis of untrusted evidence and is
	// refused for every other engine, which has no verified equivalent.
	NoTools bool
}

// Command is a prepared process launch.
type Command struct {
	Path string
	Args []string
	Env  []string
}

// Permissions accepted by Work, as in the legacy source.
const (
	PermissionReadOnly       = "read-only"
	PermissionWorkspaceWrite = "workspace-write"
)

// Command validates spec and builds the argv/environment for the engine. The
// caller owns the process, its working directory and its stdin.
func (e *Engines) Command(spec Spec) (Command, error) {
	if spec.Engine != EngineCodex && spec.Engine != EngineOMP {
		return Command{}, errors.New("Motor inválido")
	}
	profile := spec.Profile
	if profile == "" {
		profile = OMPProfileDefault
	}
	if !ompProfiles[profile] {
		return Command{}, errors.New("Perfil OMP inválido")
	}
	if spec.Engine != EngineOMP && profile != OMPProfileDefault {
		return Command{}, errors.New("El perfil solo corresponde a OMP")
	}
	if spec.Model != "" && (utf8.RuneCountInString(spec.Model) > 500 || strings.ContainsFunc(spec.Model, func(r rune) bool { return r < 32 })) {
		return Command{}, errors.New("Modelo inválido")
	}
	if spec.Engine == EngineOMP && spec.Model != "" && !RoutedModel(spec.Model) {
		return Command{}, errors.New("OMP solo puede usar modelos Magpie de este host")
	}
	if spec.Engine == EngineOMP && e.router == nil {
		return Command{}, errors.New("OMP requiere la configuración Magpie de este host")
	}
	if spec.NoTools && (spec.Engine != EngineOMP || spec.Interactive) {
		return Command{}, errors.New("Sólo una ejecución OMP no interactiva puede desactivar todas las herramientas")
	}
	// An interactive terminal is the user's own session: it has no sandbox
	// mode to select, exactly like the legacy terminal.py.
	if !spec.Interactive && spec.Permission != PermissionReadOnly && spec.Permission != PermissionWorkspaceWrite {
		return Command{}, errors.New("Permiso inválido")
	}
	if spec.Library && (spec.Engine != EngineOMP || profile != OMPProfileDefault) {
		return Command{}, errors.New("La biblioteca DFM se instala en el perfil principal")
	}
	binary := e.Resolve(spec.Engine)
	if binary == "" {
		return Command{}, errors.New("Motor no instalado")
	}
	env, err := e.launchEnv(spec, binary)
	if err != nil {
		return Command{}, err
	}
	args, err := e.arguments(spec)
	if err != nil {
		return Command{}, err
	}
	if spec.Interactive {
		env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	}
	return Command{Path: binary, Args: args, Env: env}, nil
}

func (e *Engines) arguments(spec Spec) ([]string, error) {
	if spec.Engine == EngineCodex {
		return e.codexArguments(spec)
	}
	return ompArguments(spec), nil
}

func (e *Engines) codexArguments(spec Spec) ([]string, error) {
	permission := spec.Permission
	model := spec.Model
	if e.router == nil {
		return nil, errors.New("Codex requiere la configuración Magpie de este host")
	}
	overrides, err := e.router.CodexArgs(model)
	if err != nil {
		return nil, err
	}
	if spec.Interactive {
		args := []string{}
		args = append(args, overrides...)
		if model != "" {
			args = append(args, "-m", model)
		}
		return args, nil
	}
	args := []string{"exec"}
	if spec.NativeID != "" {
		args = append(args, "resume", spec.NativeID)
	}
	args = append(args, "--json", "--skip-git-repo-check", "-c", `sandbox_mode="`+permission+`"`)
	args = append(args, overrides...)
	if model != "" {
		args = append(args, "-m", model)
	}
	return append(args, "-"), nil
}

// ompArguments mirrors the OMP flags: one-shot printing for Work and the plain
// native interface for Terminal, where the user owns /login and /model.
func ompArguments(spec Spec) []string {
	if spec.Interactive {
		args := []string{}
		if spec.Model != "" {
			args = append(args, "--model", spec.Model)
		}
		return args
	}
	args := []string{"--print", "--mode", "json", "--no-title", "--session-dir", spec.SessionDir, "--no-pty", "--max-time", "30m"}
	if spec.NativeID != "" {
		args = append(args, "--resume", spec.NativeID)
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Permission == PermissionReadOnly {
		if spec.NoTools {
			// --no-tools empties the built-in allowlist; an allowlist passed beside
			// it wins, so this is the only form that keeps every built-in off, and
			// extension, skill and rule discovery stay off with it.
			return append(args, "--no-tools", "--no-extensions", "--no-lsp", "--no-skills", "--no-rules", "--approval-mode", "yolo")
		}
		return append(args, "--tools", "read,grep,glob", "--no-extensions", "--no-lsp", "--approval-mode", "yolo")
	}
	// OMP's local CLI has no OS sandbox; workspace-write therefore runs with the
	// user's own permissions and the interface says so.
	return append(args, "--approval-mode", "yolo")
}

// launchEnv builds the explicit, private environment for one launch. The
// process never inherits the user's agent homes or credentials.
func (e *Engines) launchEnv(spec Spec, binary string) ([]string, error) {
	profile := spec.Profile
	if profile == "" {
		profile = OMPProfileDefault
	}
	env := envMap(e.env)
	// Agent discovery keeps the user's install locations; the launched process
	// gets our own homes below.
	discoveryPath := nativePath(env)
	home := e.AgentHome()
	agent := ompAgentDir(home, profile)
	values := map[string]string{
		"HOME":                home,
		"XDG_CONFIG_HOME":     filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":      filepath.Join(home, ".cache"),
		"XDG_DATA_HOME":       filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":      filepath.Join(home, ".local", "state"),
		"XDG_RUNTIME_DIR":     filepath.Join(e.state, "agents", "run"),
		"CODEX_HOME":          filepath.Join(home, ".codex"),
		"PI_CONFIG_DIR":       ".omp",
		"PI_CODING_AGENT_DIR": agent,
		"OMP_PROFILE":         profile,
		"PATH":                discoveryPath,
	}
	if err := privateDir(values["CODEX_HOME"]); err != nil {
		return nil, err
	}
	for _, name := range []string{
		"OMP_AUTH_BROKER_URL", "OMP_AUTH_BROKER_TOKEN", "PI_CONFIG_FILES", "OMP_CONFIG_FILES", "OMP_CONFIG_DIR",
		"CODEX_PORTABLE_INFERENCE_TOKEN", "CODEX_PORTABLE_OMP_BROKER_PORT", "CODEX_PORTABLE_OMP_GATEWAY_PORT",
		// Another agent's home would let its discovery (MCP servers, extensions)
		// reach the launch from outside this installation's private tree.
		"CLAUDE_CONFIG_DIR", "COPILOT_HOME",
	} {
		delete(env, name)
	}
	if spec.Engine == EngineOMP {
		delete(env, "NO_COLOR")
	}
	for name, value := range values {
		env[name] = value
	}
	if spec.Engine == EngineOMP {
		if err := e.prepareProfile(home, profile, binary, spec.Library, flattenEnv(env)); err != nil {
			return nil, err
		}
	}
	if spec.Engine == EngineOMP {
		entries, err := e.router.AgentEnv(spec.Engine, profile)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			name, value, ok := strings.Cut(entry, "=")
			if !ok {
				return nil, fmt.Errorf("el enrutado devolvió un entorno inválido: %q", entry)
			}
			env[name] = value
		}
	}
	return flattenEnv(env), nil
}

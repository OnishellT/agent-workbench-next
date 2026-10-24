package harness

import (
	"errors"
	"path/filepath"
)

// NativeCommand uses the configured user's agent and profile without creating a
// private replacement HOME or copying credentials into Workbench's state.
func (e *Engines) NativeCommand(spec Spec) (Command, error) {
	if spec.Engine != EngineCodex && spec.Engine != EngineOMP {
		return Command{}, errors.New("Motor inválido")
	}
	if spec.Profile == "" {
		spec.Profile = OMPProfileDefault
	}
	if !ompProfiles[spec.Profile] || spec.Engine == EngineCodex && spec.Profile != OMPProfileDefault {
		return Command{}, errors.New("Perfil nativo inválido")
	}
	if spec.Interactive || spec.Library || spec.NoTools || spec.NativeID != "" || spec.Model != "" {
		return Command{}, errors.New("La ejecución nativa conserva el modelo y la configuración del agente")
	}
	if spec.Permission != PermissionReadOnly && spec.Permission != PermissionWorkspaceWrite {
		return Command{}, errors.New("Permiso inválido")
	}
	binary := e.Resolve(spec.Engine)
	if binary == "" {
		return Command{}, errors.New("Motor no instalado")
	}
	env := envMap(e.env)
	if !filepath.IsAbs(env["HOME"]) {
		return Command{}, errors.New("El usuario nativo requiere HOME absoluto")
	}
	env["PATH"] = nativePath(env)
	if spec.Engine == EngineOMP {
		env["PI_CODING_AGENT_DIR"] = ompAgentDir(env["HOME"], spec.Profile)
		env["OMP_PROFILE"] = spec.Profile
	}
	var args []string
	if spec.Engine == EngineCodex {
		args = []string{"exec", "--json", "--skip-git-repo-check", "-c", `sandbox_mode="` + spec.Permission + `"`, "-"}
	} else {
		args = ompArguments(spec)
	}
	return Command{Path: binary, Args: args, Env: flattenEnv(env)}, nil
}

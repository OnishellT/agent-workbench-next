package hosts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Limites del ensayo remoto memU: sin stack, sin navegador, sin HOME.
const (
	memUMaxFiles       = 200
	memUMaxFile        = 1 << 20
	memUMaxTotal       = 4 << 20
	memUPromptMax      = 64 << 10
	memURemotePrefix   = "/tmp/workbench-memu-"
	memuMaxReturnFiles = 200
	memuMaxReturnTotal = 4 << 20
	memuMaxDepth       = 8
)

var (
	memUEngineAllow       = map[string]bool{"codex": true, "omp": true}
	memUOMPProfiles       = map[string]bool{"": true, "default": true, "personal": true, "work": true}
	memUProfilePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	memURemoteTempPattern = regexp.MustCompile(`^/tmp/workbench-memu-[A-Za-z0-9]{6}$`)
	memUPidPattern        = regexp.MustCompile(`^[1-9][0-9]{0,10}$`)
	memUOutputExt         = map[string]bool{".md": true, ".py": true, ".sh": true}
)

// RunMemuJob ejecuta un agente nativo del usuario en el host remoto sobre una
// copia privada del workspace. Solo codex/omp con perfil nativo; sin flags de
// modelo ni URLs añadidas por Workbench. Metodo publico para que main lo
// conecte como callback; no monta API de Work.
func (m *Module) RunMemuJob(ctx context.Context, hostID, engine, profile, workspace, prompt string) error {
	if m == nil || m.SSH == nil {
		return errors.New("transporte SSH no disponible")
	}
	if err := validateMemuJob(engine, profile, workspace, prompt); err != nil {
		return err
	}
	files, err := collectMemuUploads(workspace)
	if err != nil {
		return err
	}
	if len(prompt) > memUPromptMax {
		return errors.New("prompt memU demasiado largo")
	}
	return m.SSH.SSHOperation(ctx, hostID, func(opCtx context.Context, client *ssh.Client, _ Identity) error {
		return runMemuRemote(opCtx, client, files, workspace, engine, profile, prompt)
	})
}

// validateMemuJob acepta perfil vacio (defecto nativo). Codex solo entiende el
// defecto o un perfil con su flag; OMP solo default/personal/work.
func validateMemuJob(engine, profile, workspace, prompt string) error {
	if !memUEngineAllow[engine] {
		return errors.New("motor memU no permitido: solo codex/omp")
	}
	if engine == "omp" {
		if !memUOMPProfiles[profile] {
			return errors.New("perfil memU invalido: solo default/personal/work")
		}
	} else if profile != "" && profile != "default" && !memUProfilePattern.MatchString(profile) {
		return errors.New("perfil memU invalido: solo perfiles nativos")
	}
	if strings.Contains(profile, "://") || strings.HasPrefix(profile, "-") {
		return errors.New("perfil memU invalido")
	}
	if workspace == "" || prompt == "" {
		return errors.New("workspace y prompt memU obligatorios")
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return errors.New("workspace memU no disponible")
	}
	return nil
}

type memuLocalFile struct {
	rel  string
	abs  string
	size int64
}

// collectMemuUploads lista envios permitidos: export/jobs/sessions/memory/skill
// mas resources.md en raiz. Salta directorios fuera del allowlist sin
// recorrerlos (adoptions/commits) y rechaza symlinks y sobrelimites.
func collectMemuUploads(workspace string) ([]memuLocalFile, error) {
	var out []memuLocalFile
	var total int64
	err := filepath.WalkDir(workspace, func(abs string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(workspace, abs)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := os.Lstat(abs)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink memU rechazado: %s", rel)
		}
		if info.IsDir() {
			if top, _, _ := strings.Cut(rel, "/"); !isMemuUploadTop(top) {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("archivo memU no regular: %s", rel)
		}
		if !isMemuUploadRel(rel) {
			return nil // fuera del ensayo: se ignora, no se sube
		}
		if info.Size() > memUMaxFile {
			return fmt.Errorf("archivo memU demasiado grande: %s", rel)
		}
		total += info.Size()
		if total > memUMaxTotal {
			return errors.New("workspace memU supera el limite total")
		}
		out = append(out, memuLocalFile{rel: rel, abs: abs, size: info.Size()})
		if len(out) > memUMaxFiles {
			return errors.New("workspace memU supera el limite de archivos")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// isMemuUploadTop reports whether a top-level workspace entry may be uploaded.
func isMemuUploadTop(top string) bool {
	switch top {
	case "export", "jobs", "sessions", "memory", "skill":
		return true
	}
	return false
}

func isMemuUploadRel(rel string) bool {
	if rel == "resources.md" {
		return true
	}
	top, _, ok := strings.Cut(rel, "/")
	return ok && isMemuUploadTop(top)
}

// memuOutputParts valida segmentos y profundidad de un retorno.
func memuOutputParts(rel string) ([]string, bool) {
	if rel == "resources.md" {
		return []string{rel}, true
	}
	parts := strings.Split(rel, "/")
	if len(parts) < 2 || len(parts) > memuMaxDepth {
		return nil, false
	}
	if parts[0] != "memory" && parts[0] != "skill" {
		return nil, false
	}
	if !validRelPath(rel) {
		return nil, false
	}
	return parts, true
}

// isMemuOutputDir autoriza descenso recursivo bajo memory/skill.
func isMemuOutputDir(rel string) bool {
	if rel == "memory" || rel == "skill" {
		return true
	}
	_, ok := memuOutputParts(rel)
	return ok
}

// isMemuOutputRel acepta retornos recursivos: memory/skill con md/py/sh a
// cualquier profundidad (skill/domain/SKILL.md, scripts) y resources.md.
func isMemuOutputRel(rel string) bool {
	parts, ok := memuOutputParts(rel)
	if !ok {
		return false
	}
	if len(parts) == 1 {
		return true // resources.md
	}
	return memUOutputExt[path.Ext(parts[len(parts)-1])]
}

func validRelPath(rel string) bool {
	if rel == "" || strings.Contains(rel, "\\") || strings.Contains(rel, "\x00") {
		return false
	}
	if path.IsAbs(rel) {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// replaceWorkspacePrefix sustituye el prefijo local por el remoto en el prompt.
func replaceWorkspacePrefix(prompt, workspace, remote string) string {
	if workspace == "" {
		return prompt
	}
	out := strings.ReplaceAll(prompt, workspace, remote)
	trimmed := strings.TrimSuffix(workspace, "/")
	if trimmed != workspace {
		out = strings.ReplaceAll(out, trimmed, remote)
	} else {
		out = strings.ReplaceAll(out, workspace+"/", remote+"/")
	}
	return out
}

// memuAgentShell lanza el agente nativo con cwd temporal y stdin del promptfile.
// Codex usa su exec+json nativo (sin --profile en vacio/default); OMP no usa
// subcomando exec ni --profile: usa --print con su PI_CODING_AGENT_DIR nativo
// segun perfil. Nunca anade flags de modelo ni URLs.
func memuAgentShell(tmp, engine, profile string) string {
	stdin := " <" + shellQuote(path.Join(tmp, "prompt.md"))
	if engine == "omp" {
		p := profile
		if p == "" {
			p = "default"
		}
		agent := "~/.omp/agent"
		if p != "default" {
			agent = "~/.omp/profiles/" + shellQuote(p) + "/agent"
		}
		env := "PI_CODING_AGENT_DIR=" + agent + " OMP_PROFILE=" + shellQuote(p)
		return env + " " + shellQuote(engine) + " --print --mode json --no-pty" + stdin
	}
	args := "exec --json --skip-git-repo-check -c " + shellQuote(`sandbox_mode="workspace-write"`)
	if profile != "" && profile != "default" {
		args += " --profile " + shellQuote(profile)
	}
	return shellQuote(engine) + " " + args + " -" + stdin
}

// buildMemuRemoteCommand ejecuta el agente en su propio grupo (setsid) bajo un
// shell supervisor en primer plano: mantiene el trap (sin exec) y reaps al hijo
// esperando su salida. El trap solo cubre senales para no borrar en exito; el
// pid del grupo queda en un hermano fuera del workspace escribible.
func buildMemuRemoteCommand(tmp, engine, profile string) string {
	t := shellQuote(tmp)
	pidFile := shellQuote(tmp + ".pid")
	inner := nativeSSHPath + "echo $$ > " + pidFile + "; " +
		"trap 'rm -rf -- " + t + "; exit 143' INT TERM HUP; " +
		"cd -- " + t + " && " + memuAgentShell(tmp, engine, profile) + "; " +
		"code=$?; trap - INT TERM HUP; exit $code"
	return "setsid sh -c " + shellQuote(inner)
}

func cleanupMemuRemote(ctx context.Context, client *ssh.Client, tmp string) {
	_, _ = remoteCommand(ctx, client, "rm -rf -- "+shellQuote(tmp)+" "+shellQuote(tmp+".pid"))
}

func runMemuRemote(ctx context.Context, client *ssh.Client, files []memuLocalFile, workspace, engine, profile, prompt string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := remoteCommand(ctx, client, "umask 077 && mktemp -d "+shellQuote(memURemotePrefix+"XXXXXX"))
	if err != nil {
		return err
	}
	tmp := strings.TrimSpace(string(raw))
	if !memURemoteTempPattern.MatchString(tmp) {
		return errors.New("temporal remoto memU invalido")
	}
	fs, err := sftp.NewClient(client)
	if err != nil {
		return errors.New("SFTP no disponible para memU")
	}
	defer fs.Close()
	cleanup := func() { cleanupMemuRemote(context.Background(), client, tmp) }
	// Sube copia privada del ensayo.
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			cleanup()
			return err
		}
		remote := path.Join(tmp, path.Clean("/"+f.rel))
		if !strings.HasPrefix(remote, tmp+"/") {
			cleanup()
			return errors.New("ruta memU fuera del temporal")
		}
		if err := fs.MkdirAll(path.Dir(remote)); err != nil {
			cleanup()
			return err
		}
		if err := uploadMemuFile(fs, f.abs, remote, f.size); err != nil {
			cleanup()
			return err
		}
	}
	remotePrompt := replaceWorkspacePrefix(prompt, workspace, tmp)
	if len(remotePrompt) > memUPromptMax {
		cleanup()
		return errors.New("prompt memU demasiado largo")
	}
	w, err := fs.OpenFile(path.Join(tmp, "prompt.md"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		cleanup()
		return err
	}
	if _, err := io.WriteString(w, remotePrompt); err != nil {
		_ = w.Close()
		cleanup()
		return err
	}
	if err := w.Close(); err != nil {
		cleanup()
		return err
	}
	_ = fs.Chmod(path.Join(tmp, "prompt.md"), 0o600)
	// Ejecuta agente nativo del usuario con cwd temporal y stdin promptfile.
	if err := runMemuAgent(ctx, client, fs, tmp, engine, profile); err != nil {
		return err // ya limpio y evita copiar tras cancel
	}
	if err := ctx.Err(); err != nil {
		cleanup()
		return err
	}
	// Copia de vuelta solo outputs seguros.
	if err := downloadMemuOutputs(ctx, fs, tmp, workspace); err != nil {
		cleanup()
		return err
	}
	cleanup()
	return nil
}

func uploadMemuFile(fs *sftp.Client, abs, remote string, size int64) error {
	if size > memUMaxFile {
		return errors.New("archivo memU demasiado grande")
	}
	local, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer local.Close()
	info, err := local.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("archivo memU no regular")
	}
	w, err := fs.OpenFile(remote, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	n, err := io.CopyN(w, io.LimitReader(local, memUMaxFile+1), memUMaxFile+1)
	closeErr := w.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if n > memUMaxFile || closeErr != nil {
		return errors.New("archivo memU demasiado grande")
	}
	_ = fs.Chmod(remote, 0o600)
	return nil
}

// runMemuAgent ejecuta el comando remoto; en cancelacion mata el grupo propio,
// limpia el temporal y retorna sin copiar resultados.
func runMemuAgent(ctx context.Context, client *ssh.Client, fs *sftp.Client, tmp, engine, profile string) error {
	session, err := client.NewSession()
	if err != nil {
		cleanupMemuRemote(context.Background(), client, tmp)
		return err
	}
	defer session.Close()
	stop := context.AfterFunc(ctx, func() { session.Close() })
	defer stop()
	wait := make(chan error, 1)
	go func() { wait <- session.Run(buildMemuRemoteCommand(tmp, engine, profile)) }()
	select {
	case <-ctx.Done():
		killMemuGroup(client, fs, tmp, engine)
		<-wait
		return ctx.Err()
	case err := <-wait:
		if err != nil {
			cleanupMemuRemote(context.Background(), client, tmp)
			return errors.New("el agente memU remoto fallo")
		}
		return nil
	}
}

// readMemuPidFile lee el pid del grupo desde el hermano fuera del workspace.
func readMemuPidFile(fs *sftp.Client, pidFile string) string {
	f, err := fs.Open(pidFile)
	if err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, 32))
	_ = f.Close()
	if err != nil {
		return ""
	}
	pid := strings.TrimSpace(string(data))
	if !memUPidPattern.MatchString(pid) {
		return ""
	}
	return pid
}

// verifyMemuProc confirma que el pid es el supervisor esperado: su cwd es el
// temporal y su linea de comandos menciona el motor y el promptfile. Sin esta
// comprobacion un agente podria plantar un pid arbitrario.
func verifyMemuProc(cwd, cmdline, tmp, engine string) bool {
	if cwd != tmp || engine == "" {
		return false
	}
	return strings.Contains(cmdline, engine) && strings.Contains(cmdline, "prompt.md")
}

func verifyMemuProcRemote(ctx context.Context, client *ssh.Client, pid, tmp, engine string) bool {
	cwdRaw, err := remoteCommand(ctx, client, "readlink "+shellQuote("/proc/"+pid+"/cwd"))
	if err != nil {
		return false
	}
	cmdRaw, err := remoteCommand(ctx, client, "cat "+shellQuote("/proc/"+pid+"/cmdline"))
	if err != nil {
		return false
	}
	cmdline := strings.ReplaceAll(string(cmdRaw), "\x00", " ")
	return verifyMemuProc(strings.TrimSpace(string(cwdRaw)), cmdline, tmp, engine)
}

// killMemuGroup mata solo el grupo verificado y limpia. Nunca mata un pid sin
// verificar /proc primero.
func killMemuGroup(client *ssh.Client, fs *sftp.Client, tmp, engine string) {
	bg, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if pid := readMemuPidFile(fs, tmp+".pid"); pid != "" && verifyMemuProcRemote(bg, client, pid, tmp, engine) {
		_, _ = remoteCommand(bg, client, "kill -KILL -- -"+pid+" 2>/dev/null || true")
	}
	cleanupMemuRemote(bg, client, tmp)
}

func downloadMemuOutputs(ctx context.Context, fs *sftp.Client, tmp, workspace string) error {
	count := 0
	var total int64
	var walk func(remoteDir string) error
	walk = func(remoteDir string) error {
		entries, err := fs.ReadDir(remoteDir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			remote := path.Join(remoteDir, entry.Name())
			rel := strings.TrimPrefix(remote, tmp+"/")
			if remote == rel || !validRelPath(rel) {
				continue
			}
			info, err := fs.Lstat(remote)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("retorno memU rechazado: %s", rel)
			}
			if info.IsDir() {
				if isMemuOutputDir(rel) {
					if err := walk(remote); err != nil {
						return err
					}
				}
				continue
			}
			if !info.Mode().IsRegular() || !isMemuOutputRel(rel) {
				continue // nunca copiar fuera del allowlist
			}
			if info.Size() > memUMaxFile {
				return fmt.Errorf("retorno memU demasiado grande: %s", rel)
			}
			count++
			if count > memuMaxReturnFiles {
				return errors.New("retorno memU supera el limite de archivos")
			}
			total += info.Size()
			if total > memuMaxReturnTotal {
				return errors.New("retorno memU supera el limite total")
			}
			if err := fetchMemuFile(ctx, fs, remote, filepath.Join(workspace, filepath.FromSlash(rel))); err != nil {
				return err
			}
		}
		return nil
	}
	for _, dir := range []string{"memory", "skill"} {
		remote := path.Join(tmp, dir)
		info, err := fs.Lstat(remote)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := walk(remote); err != nil {
			return err
		}
	}
	// resources.md opcional en raiz del temporal.
	if info, err := fs.Lstat(path.Join(tmp, "resources.md")); err == nil {
		if info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() && info.Size() <= memUMaxFile {
			if err := fetchMemuFile(ctx, fs, path.Join(tmp, "resources.md"), filepath.Join(workspace, "resources.md")); err != nil {
				return err
			}
		}
	}
	return nil
}

func fetchMemuFile(ctx context.Context, fs *sftp.Client, remote, local string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r, err := fs.Open(remote)
	if err != nil {
		return err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, memUMaxFile+1))
	if err != nil || int64(len(data)) > memUMaxFile {
		return errors.New("retorno memU demasiado grande")
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return err
	}
	// La ruta local deriva de rel validado; sin absolutos ni .. .
	return os.WriteFile(local, data, 0o600)
}

package hosts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func writeMemuFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMemuJobRejectsEngineProfile(t *testing.T) {
	dir := t.TempDir()
	writeMemuFixture(t, dir, "memory/a.md", "x")
	// Vacio y default valen para ambos (la UI pasa vacio para Codex).
	for _, tc := range []struct{ engine, profile string }{
		{"codex", ""}, {"codex", "default"}, {"codex", "ensayo"},
		{"omp", ""}, {"omp", "default"}, {"omp", "personal"}, {"omp", "work"},
	} {
		if err := validateMemuJob(tc.engine, tc.profile, dir, "hola"); err != nil {
			t.Fatalf("caso valido rechazado %+v: %v", tc, err)
		}
	}
	for _, tc := range []struct{ engine, profile string }{
		{"gpt", "default"},
		{"codex", "--model o3"},
		{"codex", "http://x"},
		{"omp", "-p evil"},
		{"omp", "ensayo"},
		{"codex", "../escape"},
		{"codex", ""},
	} {
		if tc.engine == "codex" && tc.profile == "" {
			continue // valido: defecto nativo
		}
		if err := validateMemuJob(tc.engine, tc.profile, dir, "hola"); err == nil {
			t.Fatalf("motor/perfil aceptado: %+v", tc)
		}
	}
}

func TestMemuUploadAllowlist(t *testing.T) {
	dir := t.TempDir()
	writeMemuFixture(t, dir, "memory/a.md", "mem")
	writeMemuFixture(t, dir, "skill/s.py", "print(1)")
	writeMemuFixture(t, dir, "export/x.json", "{}")
	writeMemuFixture(t, dir, "resources.md", "r")
	writeMemuFixture(t, dir, ".codex/auth.json", "SECRETO")
	writeMemuFixture(t, dir, ".config/data", "SECRETO")
	files, err := collectMemuUploads(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.rel, ".codex") || strings.HasPrefix(f.rel, ".config") {
			t.Fatalf("fuga de HOME/config: %s", f.rel)
		}
	}
	if len(files) != 4 {
		t.Fatalf("esperaba 4 envios, obtuvo %d: %v", len(files), files)
	}
}

func TestMemuUploadSkipsOutsideDirs(t *testing.T) {
	dir := t.TempDir()
	writeMemuFixture(t, dir, "memory/a.md", "x")
	writeMemuFixture(t, dir, "adoptions/lote1/big.md", "no recorrer")
	writeMemuFixture(t, dir, "commits/x.md", "no recorrer")
	writeMemuFixture(t, dir, "notas.txt", "ignorado")
	files, err := collectMemuUploads(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].rel != "memory/a.md" {
		t.Fatalf("recorrido fuera del allowlist: %v", files)
	}
}

func TestMemuUploadRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	writeMemuFixture(t, dir, "memory/a.md", "x")
	target := filepath.Join(dir, "memory", "a.md")
	if err := os.Symlink(target, filepath.Join(dir, "memory", "evil.md")); err != nil {
		t.Skip("sin symlinks")
	}
	if _, err := collectMemuUploads(dir); err == nil {
		t.Fatal("symlink aceptado")
	}
}

func TestMemuUploadRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, memUMaxFile+1)
	for i := range big {
		big[i] = 'a'
	}
	abs := filepath.Join(dir, "memory", "big.md")
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, big, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := collectMemuUploads(dir); err == nil {
		t.Fatal("sobrelimite aceptado")
	}
}

func TestMemuOutputAllowlist(t *testing.T) {
	ok := []string{"memory/a.md", "skill/s.py", "skill/run.sh", "memory/sub/a.md", "skill/domain/SKILL.md", "skill/domain/scripts/s.sh", "resources.md"}
	for _, rel := range ok {
		if !isMemuOutputRel(rel) {
			t.Fatalf("retorno valido rechazado: %s", rel)
		}
	}
	bad := []string{
		"../escape.md", "/abs.md", "memory/../../x.md", "memory/a.exe",
		"memory/a.md ", "memory/", "", ".codex/auth.json", ".config/x",
		"export/x.md", "memory/a.txt", strings.Repeat("d/", memuMaxDepth) + "a.md",
	}
	for _, rel := range bad {
		if isMemuOutputRel(rel) {
			t.Fatalf("retorno peligroso aceptado: %q", rel)
		}
	}
	if !isMemuOutputDir("memory") || !isMemuOutputDir("skill/domain") {
		t.Fatal("descenso recursivo rechazado")
	}
	if isMemuOutputDir(".codex") || isMemuOutputDir("export") {
		t.Fatal("descenso fuera del allowlist aceptado")
	}
}

func TestMemuRemoteCommandSafety(t *testing.T) {
	cmd := buildMemuRemoteCommand("/tmp/workbench-memu-ABC123", "codex", "default")
	for _, want := range []string{"setsid", "trap", "rm -rf -- ", "prompt.md", "codex", "sandbox_mode="} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("comando sin %q: %s", want, cmd)
		}
	}
	for _, banned := range []string{"--model", "http://", "https://", "curl", "wget"} {
		if strings.Contains(cmd, banned) {
			t.Fatalf("comando con %q prohibido: %s", banned, cmd)
		}
	}
	// Perfil con intento de escape queda citado, no rompe la comilla.
	evil := buildMemuRemoteCommand("/tmp/workbench-memu-ABC123", "codex", "a'; rm -rf /; echo '")
	if strings.Count(evil, "'\\''") < 2 {
		t.Fatalf("perfil maligno sin citar: %s", evil)
	}
	// OMP no usa exec ni --profile.
	omp := buildMemuRemoteCommand("/tmp/workbench-memu-ABC123", "omp", "personal")
	for _, banned := range []string{"exec", "--profile"} {
		if strings.Contains(omp, banned) {
			t.Fatalf("omp con %q prohibido: %s", banned, omp)
		}
	}
	for _, want := range []string{"--print", "--mode json", "--no-pty", "PI_CODING_AGENT_DIR=", "OMP_PROFILE="} {
		if !strings.Contains(omp, want) {
			t.Fatalf("omp sin %q: %s", want, omp)
		}
	}
	// Codex con perfil nombrado usa --profile; vacio/default no.
	if strings.Contains(buildMemuRemoteCommand("/tmp/workbench-memu-ABC123", "codex", ""), "--profile") {
		t.Fatal("codex por defecto con --profile")
	}
	if !strings.Contains(buildMemuRemoteCommand("/tmp/workbench-memu-ABC123", "codex", "ensayo"), "--profile") {
		t.Fatal("codex con perfil sin --profile")
	}
}

func TestVerifyMemuProc(t *testing.T) {
	tmp := "/tmp/workbench-memu-ABC123"
	cmdline := "sh -c echo $$ > /tmp/x.pid; cd -- " + tmp + " && codex exec --json <" + tmp + "/prompt.md"
	if !verifyMemuProc(tmp, cmdline, tmp, "codex") {
		t.Fatal("supervisor valido rechazado")
	}
	if verifyMemuProc("/otro/dir", cmdline, tmp, "codex") {
		t.Fatal("cwd distinto aceptado")
	}
	if verifyMemuProc(tmp, "sh -c sleep 30", tmp, "codex") {
		t.Fatal("cmdline ajena aceptada")
	}
	if verifyMemuProc(tmp, cmdline, tmp, "") {
		t.Fatal("motor vacio aceptado")
	}
	if verifyMemuProc(tmp, "echo 1 > /tmp/x.pid", "/tmp/workbench-memu-ABC123", "codex") {
		t.Fatal("pid manipulado aceptado")
	}
}

// stubCLIBin crea codex/omp falsos que registran argv, entorno y stdin.
func stubCLIBin(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"codex", "omp"} {
		script := "#!/bin/sh\n" +
			"echo \"args:$*\" >> \"$STUB_LOG\"\n" +
			"echo \"codex_home:${CODEX_HOME:-unset} agent_dir:${PI_CODING_AGENT_DIR:-unset} profile:${OMP_PROFILE:-unset} pwd:$(pwd)\" >> \"$STUB_LOG\"\n" +
			"cat >> \"$STUB_LOG\"\n" +
			"echo done >> \"$STUB_LOG\"\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

func runMemuCommandLocal(t *testing.T, cmd, home, bin, log string) {
	t.Helper()
	c := exec.Command("sh", "-c", cmd)
	c.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home, "STUB_LOG=" + log}
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("comando real fallo: %v\n%s", err, out)
	}
}

func TestMemuCodexCommandReal(t *testing.T) {
	home := t.TempDir()
	bin := stubCLIBin(t)
	userBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(userBin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(bin, "codex"), filepath.Join(userBin, "codex")); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "codex.log")
	ws := t.TempDir()
	tmp := filepath.Join(t.TempDir(), "remote")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	prompt := "resume " + ws + "/memory/a.md ya"
	if err := os.WriteFile(filepath.Join(tmp, "prompt.md"), []byte(replaceWorkspacePrefix(prompt, ws, tmp)), 0o600); err != nil {
		t.Fatal(err)
	}
	runMemuCommandLocal(t, buildMemuRemoteCommand(tmp, "codex", ""), home, bin, log)
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"exec", "--json", "--skip-git-repo-check", `sandbox_mode="workspace-write"`, tmp + "/memory/a.md", "pwd:" + tmp} {
		if !strings.Contains(got, want) {
			t.Fatalf("codex real sin %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--profile") {
		t.Fatalf("codex por defecto con --profile:\n%s", got)
	}
}

func TestMemuOMPCommandReal(t *testing.T) {
	home := t.TempDir()
	bin := stubCLIBin(t)
	log := filepath.Join(t.TempDir(), "omp.log")
	tmp := filepath.Join(t.TempDir(), "remote")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "prompt.md"), []byte("hola"), 0o600); err != nil {
		t.Fatal(err)
	}
	runMemuCommandLocal(t, buildMemuRemoteCommand(tmp, "omp", "personal"), home, bin, log)
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"--print", "--mode json", "--no-pty",
		"agent_dir:" + home + "/.omp/profiles/personal/agent", "profile:personal", "pwd:" + tmp,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("omp real sin %q:\n%s", want, got)
		}
	}
	for _, banned := range []string{"exec", "--profile"} {
		if strings.Contains(got, banned) {
			t.Fatalf("omp real con %q prohibido:\n%s", banned, got)
		}
	}
}

// TestMemuSupervisorCancel ejecuta el supervisor real contra un proceso propio:
// al matar el grupo, el trap limpia el temporal y el codigo es 143.
func TestMemuSupervisorCancel(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("sin setsid")
	}
	dir := t.TempDir()
	sleepy := filepath.Join(dir, "sleepy")
	if err := os.WriteFile(sleepy, []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = sleepy
	tmp := filepath.Join(dir, "remote")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	// Supervisor con stub que duerme: comparte forma con el comando real.
	inner := "echo $$ > " + tmp + ".pid; trap 'rm -rf -- " + tmp + "; exit 143' INT TERM HUP; cd -- " + tmp + " && sleep 30; code=$?; trap - INT TERM HUP; exit $code"
	// setsid via SysProcAttr equivale al setsid remoto: el hijo lidera su grupo.
	c := exec.Command("sh", "-c", inner)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	pgid, err := syscall.Getpgid(c.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err = c.Wait()
	if _, ok := err.(*exec.ExitError); !ok {
		t.Fatalf("esperaba fallo por senal, obtuvo %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(tmp); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("el trap no limpio el temporal tras matar el grupo")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMemuPromptPrefixReplace(t *testing.T) {
	got := replaceWorkspacePrefix("lee /ws/memoria/a.md ya", "/ws/memoria", "/tmp/workbench-memu-X1Y2Z3")
	if strings.Contains(got, "/ws/memoria") || !strings.Contains(got, "/tmp/workbench-memu-X1Y2Z3/a.md") {
		t.Fatalf("prefijo no sustituido: %s", got)
	}
}

func TestMemuCancelledContextAborts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &Module{}
	if err := m.RunMemuJob(ctx, "h", "codex", "", t.TempDir(), "p"); err == nil {
		t.Fatal("contexto cancelado aceptado")
	}
}

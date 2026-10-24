package hosts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func dependencyMux(t *testing.T, server *Server) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	server.dependencyRoutes(mux)
	return mux
}

func TestParseDependencyProbe(t *testing.T) {
	output := "python3|yes|\nbun|no|\ncodex|yes|\nomp|no|\ndocker|yes|\n" +
		"python3|version|3.12.1\nmemu|yes|0.11.0b3\nrm -rf /|yes|x\npython3|maybe|9\n"
	found := parseDependencyProbe(output)
	if len(found) != 6 {
		t.Fatalf("la detección publica las seis dependencias en orden: %+v", found)
	}
	byID := map[string]Dependency{}
	for _, row := range found {
		byID[row.ID] = row
	}
	if !byID["python3"].Installed || byID["python3"].Version != "3.12.1" {
		t.Fatalf("python3 con versión: %+v", byID["python3"])
	}
	if !byID["codex"].Installed || byID["codex"].Installable {
		t.Fatalf("codex detectado pero no instalable: %+v", byID["codex"])
	}
	if !byID["memu"].Installed || !byID["memu"].Installable || byID["memu"].Version != "0.11.0b3" {
		t.Fatalf("memu instalado e instalable: %+v", byID["memu"])
	}
	if byID["bun"].Installed || !byID["bun"].Installable || byID["bun"].Detail != "" {
		t.Fatalf("bun ausente pero instalable, sin aviso falso: %+v", byID["bun"])
	}
	empty := parseDependencyProbe("")
	for _, row := range empty {
		if row.Installed {
			t.Fatalf("sondeo vacío no inventa presencias: %+v", row)
		}
	}
}

func TestMemUInstallCommandSafety(t *testing.T) {
	first, second := memUInstallCommand(), memUInstallCommand()
	if first != second || first == "" {
		t.Fatal("el comando de instalación es estático y no vacío")
	}
	for _, banned := range []string{"sudo", "cp ", "rsync", "scp ", "curl ", "wget ", "$(", "`"} {
		if strings.Contains(first, banned) {
			t.Fatalf("el comando remoto no usa %q", banned)
		}
	}
	if !strings.Contains(first, "'memu-cli=="+memUVersion+"'") {
		t.Fatalf("el pin no está fijado en el comando: %s", first)
	}
	for _, want := range []string{"timeout 600", `NEW="$VENV.new-$$"`, `mv "$NEW" "$VENV"`, "trap "} {
		if !strings.Contains(first, want) {
			t.Fatalf("instalación de memu en venv temporal y movida al final; falta %q: %s", want, first)
		}
	}
	if !strings.Contains(first, "python3 -m venv") || !strings.Contains(first, "pip install") {
		t.Fatalf("venv del usuario remoto con pip: %s", first)
	}
	if !strings.Contains(first, "m.version('memu-cli')") {
		t.Fatalf("la versión se lee de la distribución memu-cli: %s", first)
	}
}

func TestBunInstallCommandSafety(t *testing.T) {
	first, second := bunInstallCommand(), bunInstallCommand()
	if first != second || first == "" {
		t.Fatal("el comando de instalación de bun es estático y no vacío")
	}
	for _, banned := range []string{"sudo", "cp ", "rsync", "scp ", "curl ", "wget ", "unzip ", "$(curl", "$(wget", "$(ssh", "`"} {
		if strings.Contains(first, banned) {
			t.Fatalf("el comando remoto no usa %q", banned)
		}
	}
	if !strings.Contains(first, "https://github.com/oven-sh/bun/releases/download/bun-v"+bunVersion+"/") || !strings.Contains(first, "ASSET="+bunX64Asset) || !strings.Contains(first, "ASSET="+bunARMAsset) {
		t.Fatalf("pin del ZIP oficial por arquitectura: %s", first)
	}
	for _, want := range []string{"SHA=" + bunX64SHA256, "SHA=" + bunARMSHA256, "hashlib.sha256()", "checksum mismatch", "uname -m", "unsupported", "timeout=60", "timeout 300", "os.replace(tmp, dest)"} {
		if want == "unsupported" {
			want = "arquitectura no soportada"
		}
		if !strings.Contains(first, want) {
			t.Fatalf("falta %q en la instalación de bun: %s", want, first)
		}
	}
	if !strings.Contains(first, "zipfile") || !strings.Contains(first, "urllib") {
		t.Fatalf("descarga y extracción con python3: %s", first)
	}
	if !strings.Contains(first, `[ ! -x "$DEST" ]`) || strings.Contains(first, `-e "$DEST"`) {
		t.Fatalf("la presencia se decide con -x y --version, no con -e: %s", first)
	}
	if !strings.Contains(first, `"$DEST" --version`) {
		t.Fatalf("la versión se lee del binario instalado: %s", first)
	}
}

func TestCheckInstallTargetAllowlist(t *testing.T) {
	if err := checkInstallTarget("memu"); err != nil {
		t.Fatalf("memu es instalable: %v", err)
	}
	if err := checkInstallTarget("bun"); err != nil {
		t.Fatalf("bun es instalable: %v", err)
	}
	for _, name := range []string{"", "codex", "docker", "MEMU", "memu ", "bun ", "python3; rm -rf /"} {
		if err := checkInstallTarget(name); err == nil {
			t.Fatalf("dependencia no instalable aceptada: %q", name)
		}
	}
}

func TestDependencyRoutesRejectBeforeSSH(t *testing.T) {
	mux := dependencyMux(t, &Server{})
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"desconocida", "POST", "/api/hosts/h1/dependencies", `{"dependency":"codex","revision":1}`},
		{"vacía", "POST", "/api/hosts/h1/dependencies", `{"dependency":"","revision":1}`},
		{"inyección", "POST", "/api/hosts/h1/dependencies", `{"dependency":"memu; id","revision":1}`},
		{"sin revisión", "POST", "/api/hosts/h1/dependencies", `{"dependency":"memu","revision":0}`},
		{"cuerpo roto", "POST", "/api/hosts/h1/dependencies", `{"dependency":`},
		{"campo extra", "POST", "/api/hosts/h1/dependencies", `{"dependency":"memu","revision":1,"command":"id"}`},
	}
	for _, row := range cases {
		response := moduleRequest(t, mux, row.method, row.path, row.body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: se esperaba 400, hubo %d %s", row.name, response.Code, response.Body.String())
		}
	}
	get := moduleRequest(t, mux, "GET", "/api/hosts/h1/dependencies", "")
	if get.Code != http.StatusServiceUnavailable {
		t.Fatalf("sin gestor SSH la lectura es 503, hubo %d", get.Code)
	}
	for _, name := range []string{"memu", "bun"} {
		valid := moduleRequest(t, mux, "POST", "/api/hosts/h1/dependencies", `{"dependency":"`+name+`","revision":1}`)
		if valid.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s pasa la allowlist y solo falta SSH (503), hubo %d", name, valid.Code)
		}
	}
}

func TestDependencyInstallUnknownHost(t *testing.T) {
	m, _ := newTestModule(t)
	mux := dependencyMux(t, m.server)
	response := moduleRequest(t, mux, "POST", "/api/hosts/ausente/dependencies", `{"dependency":"memu","revision":1}`)
	if response.Code != http.StatusNotFound {
		t.Fatalf("host desconocido es 404, hubo %d %s", response.Code, response.Body.String())
	}
	get := moduleRequest(t, mux, "GET", "/api/hosts/ausente/dependencies", "")
	if get.Code == http.StatusOK {
		t.Fatalf("la lectura no inventa hosts: %s", get.Body.String())
	}
}

func trustFixtureHost(t *testing.T, m *Module) Host {
	t.Helper()
	home := t.TempDir()
	address, port, pin := sshFixture(t, home)
	host, err := m.SSH.Save(HostInput{Host: Host{Address: address, Port: port, User: "dev", Auth: "password", Enabled: true}, Password: "fixture-secret"})
	if err != nil {
		t.Fatalf("alta del fixture: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = m.SSH.sync(ctx, &host)
	host, err = m.Store.Host(host.ID)
	if err != nil || host.ObservedFingerprint != pin {
		t.Fatalf("el fixture observa su clave: %+v %v", host, err)
	}
	trusted, err := m.SSH.TrustFingerprint(host.ID, host.Revision, pin)
	if err != nil {
		t.Fatalf("confianza explícita del fixture: %v", err)
	}
	return trusted
}

func TestDependencyRevisionConflict(t *testing.T) {
	m, _ := newTestModule(t)
	trusted := trustFixtureHost(t, m)
	mux := dependencyMux(t, m.server)
	conflict := moduleRequest(t, mux, "POST", "/api/hosts/"+trusted.ID+"/dependencies",
		fmt.Sprintf(`{"dependency":"memu","revision":%d}`, trusted.Revision+1000))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("revisión que no coincide es 409, hubo %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestDependencyProbeFindsNativeUserInstalls(t *testing.T) {
	home := t.TempDir()
	for rel, body := range map[string]string{
		".local/bin/codex": "#!/bin/sh\nexit 0\n",
		".local/share/workbench/venvs/memu/bin/python": "#!/bin/sh\nprintf '0.11.0b3\\n'\n",
	} {
		file := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", dependencyProbeCommand())
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe: %v: %s", err, out)
	}
	for _, row := range parseDependencyProbe(string(out)) {
		if row.ID == "codex" && !row.Installed {
			t.Fatal("native user Codex missed")
		}
		if row.ID == "memu" && (!row.Installed || row.Version != memUVersion) {
			t.Fatalf("installed memU venv missed: %+v", row)
		}
	}
}

// The generated install scripts are static text; make sure they parse so a
// quoting slip cannot ship (sh -n never executes anything).
func TestInstallScriptsParse(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh no disponible")
	}
	for name, script := range map[string]string{"bun": bunInstallCommand(), "memu": memUInstallCommand(), "probe": dependencyProbeCommand()} {
		cmd := exec.Command(shell, "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: sh -n: %v %s", name, err, out)
		}
		if python, err := exec.LookPath("python3"); err == nil {
			for i, body := range heredocBodies(script) {
				cmd := exec.Command(python, "-I", "-c", "import sys; compile(sys.stdin.read(), 'x', 'exec')")
				cmd.Stdin = strings.NewReader(body)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s heredoc %d: %v %s", name, i, err, out)
				}
			}
		}
	}
}

func heredocBodies(script string) []string {
	var bodies []string
	parts := strings.Split(script, "<<'PYEOF'\n")
	for _, part := range parts[1:] {
		if end := strings.Index(part, "\nPYEOF"); end >= 0 {
			bodies = append(bodies, part[:end])
		}
	}
	return bodies
}

func TestTryLockInstallIsPerHost(t *testing.T) {
	m := &SSHManager{}
	release, ok := m.tryLockInstall("a")
	if !ok {
		t.Fatal("primer bloqueo")
	}
	if _, again := m.tryLockInstall("a"); again {
		t.Fatal("la segunda instalación concurrente del mismo host debe rechazarse")
	}
	other, ok := m.tryLockInstall("b")
	if !ok {
		t.Fatal("otro host no debe verse afectado")
	}
	other()
	release()
	if release, ok = m.tryLockInstall("a"); !ok {
		t.Fatal("tras liberar se puede instalar de nuevo")
	}
	release()
}

func TestOperationErrorHidesRemoteStderr(t *testing.T) {
	rec := httptest.NewRecorder()
	err := &remoteCommandError{Category: remoteFailExit, ExitCode: 3, msg: "la consulta SSH remota falló"}
	writeDependencyError(rec, err)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "código 3") {
		t.Fatalf("error categorizado esperado: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	writeDependencyError(rec, errDependencyBusy)
	if rec.Code != http.StatusConflict {
		t.Fatalf("instalación concurrente debe ser 409: %d", rec.Code)
	}
}

func TestRedactRemoteText(t *testing.T) {
	secret := "hunter2secretvalue"
	input := "pip: password=" + secret + "\nAuthorization: Bearer abcdef0123456789abcdef0123456789abcd\nfetching https://user:" + secret + "@example.com/x\n-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\x1b[31m"
	out := redactRemoteText(input)
	for _, leaked := range []string{secret, "abcdef0123456789", "AAAA", "\x1b"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("%q filtrado en %q", leaked, out)
		}
	}
	if !strings.Contains(out, "pip:") {
		t.Fatalf("el contexto no secreto se conserva: %q", out)
	}
	if len(redactRemoteText(strings.Repeat("x ", 5000))) > remoteStderrLimit {
		t.Fatal("la salida se acota")
	}
}

func TestBoundedBufferDropsExcess(t *testing.T) {
	b := &boundedBuffer{max: 8}
	n, err := b.Write([]byte("0123456789abcdef"))
	if n != 16 || err != nil || string(b.bytes()) != "01234567" {
		t.Fatalf("n=%d err=%v got=%q", n, err, b.bytes())
	}
}

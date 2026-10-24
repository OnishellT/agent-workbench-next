package hosts

// Dependencias de ejecución por host (V2): detección de python3, bun, codex,
// omp, docker y memu, e instalación de memu (distribución PyPI memu-cli, pin
// 0.11.0b3 en un venv del propio usuario remoto) y de bun (pin 1.4.2, ZIP
// oficial en ~/.bun), sin sudo y sin copiar el HOME.
//
// La escritura nunca acepta comandos del navegador: el cuerpo solo elige dentro
// de la allowlist ("memu", "bun") y cada comando remoto es estático. Cada operación pasa
// por SSHOperation (identidad SSH confirmada + revisión) y devuelve
// resultado/progreso por host.

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Pins instalables. No salen del servidor: el navegador solo nombra la
// dependencia, nunca una versión, una URL ni un comando.
const memUVersion = "0.11.0b3"

const bunVersion = "1.4.2"

// dependencyIDs es el orden canónico de la detección.
var dependencyIDs = []string{"python3", "bun", "codex", "omp", "docker", "memu"}

var (
	errDependencyConflict = errors.New("la revisión del host cambió; vuelve a revisar la identidad")
	errDependencyBusy     = errors.New("ya hay una instalación de dependencias en curso en este host")
)

// Dependency es una dependencia conocida de un host: presencia, versión cuando
// se sondea sin riesgo, y si Workbench sabe instalarla (memu y bun).
type Dependency struct {
	ID          string `json:"id"`
	Installed   bool   `json:"installed"`
	Version     string `json:"version,omitempty"`
	Installable bool   `json:"installable"`
	Detail      string `json:"detail,omitempty"`
}

type dependenciesResponse struct {
	HostID       string       `json:"host_id"`
	Dependencies []Dependency `json:"dependencies"`
}

type installDependencyInput struct {
	Dependency string `json:"dependency"`
	Revision   int64  `json:"revision"`
}

type installDependencyResult struct {
	HostID     string `json:"host_id"`
	Dependency string `json:"dependency"`
	Version    string `json:"version,omitempty"`
	Installed  bool   `json:"installed"`
	Detail     string `json:"detail,omitempty"`
}

// dependencyRoutes publica la superficie V2. El coordinador la monta en
// hostRoutes (hosts_server.go); este archivo no toca el montaje existente.
func (s *Server) dependencyRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/hosts/{id}/dependencies", s.hostDependenciesRoute)
	m.HandleFunc("POST /api/hosts/{id}/dependencies", s.hostDependencyInstallRoute)
}

func (s *Server) hostDependenciesRoute(w http.ResponseWriter, r *http.Request) {
	if s.SSH == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("gestor SSH no disponible"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	var found []Dependency
	err := s.SSH.SSHOperation(ctx, r.PathValue("id"), func(opCtx context.Context, client *ssh.Client, _ Identity) error {
		out, err := remoteCommand(opCtx, client, dependencyProbeCommand())
		if err != nil {
			return err
		}
		found = parseDependencyProbe(string(out))
		return nil
	})
	if err != nil {
		writeDependencyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dependenciesResponse{HostID: r.PathValue("id"), Dependencies: found})
}

func (s *Server) hostDependencyInstallRoute(w http.ResponseWriter, r *http.Request) {
	var input installDependencyInput
	if err := decodeStrictRequest(w, r, 1<<20, &input); err != nil {
		fail(w, http.StatusBadRequest, errors.New("solicitud de instalación inválida"))
		return
	}
	if err := checkInstallTarget(input.Dependency); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if input.Revision < 1 {
		fail(w, http.StatusBadRequest, errors.New("revisión del host obligatoria"))
		return
	}
	if s.SSH == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("gestor SSH no disponible"))
		return
	}
	// One install per host at a time: concurrent POSTs would race on the same
	// remote venv/~/.bun paths.
	release, ok := s.SSH.tryLockInstall(r.PathValue("id"))
	if !ok {
		writeDependencyError(w, errDependencyBusy)
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	result := installDependencyResult{HostID: r.PathValue("id"), Dependency: input.Dependency}
	err := s.SSH.SSHOperation(ctx, result.HostID, func(opCtx context.Context, client *ssh.Client, identity Identity) error {
		if identity.Revision != input.Revision {
			return errDependencyConflict
		}
		command, detail := installCommandFor(input.Dependency)
		out, err := remoteCommand(opCtx, client, command)
		if err != nil {
			return err
		}
		result.Version = strings.TrimSpace(string(out))
		result.Installed = result.Version != ""
		result.Detail = detail
		return nil
	})
	if err != nil {
		writeDependencyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// installCommandFor elige el comando estático de la dependencia. Solo se llama
// con un nombre que pasó checkInstallTarget.
func installCommandFor(name string) (command, detail string) {
	if name == "bun" {
		return bunInstallCommand(), "Bun en ~/.bun del usuario remoto (se conserva una instalación existente)"
	}
	return memUInstallCommand(), "memu " + memUVersion + " en el venv del usuario remoto"
}

// installableDependency reports whether Workbench knows how to install id.
func installableDependency(id string) bool { return id == "memu" || id == "bun" }

// checkInstallTarget es la allowlist de escritura: memu y bun. Cualquier otro
// nombre (incluidas las demás dependencias detectadas) se rechaza sin tocar SSH.
func checkInstallTarget(name string) error {
	if installableDependency(name) {
		return nil
	}
	return errors.New("dependencia no instalable desde Workbench: solo memu y bun")
}

// dependencyProbeCommand sondea presencia sin ejecutar las herramientas: solo
// `command -v` (más ~/.bun/bin/bun, que no está en el PATH), la versión de
// python3 y la de memu vía importlib de la distribución memu-cli. Estático:
// sin interpolación de nada que venga del navegador o del host.
func dependencyProbeCommand() string {
	return nativeSSHPath + `for d in python3 codex omp docker; do if command -v "$d" >/dev/null 2>&1; then printf '%s|yes|\n' "$d"; else printf '%s|no|\n' "$d"; fi; done
if command -v bun >/dev/null 2>&1; then printf 'bun|yes|\n'; elif [ -x "$HOME/.bun/bin/bun" ]; then printf 'bun|yes|%s\n' "$("$HOME/.bun/bin/bun" --version 2>/dev/null)"; else printf 'bun|no|\n'; fi
python3 --version 2>&1 | sed -n 's/^Python /python3|version|/p'
MEMU_PY="${XDG_DATA_HOME:-$HOME/.local/share}/workbench/venvs/memu/bin/python"
[ -x "$MEMU_PY" ] || MEMU_PY=python3
v=$("$MEMU_PY" -c "import importlib.metadata as m; print(m.version('memu-cli'))" 2>/dev/null) && printf 'memu|yes|%s\n' "$v" || printf 'memu|no|\n'`
}

// memUInstallCommand instala el pin de la distribución memu-cli en un venv del
// usuario remoto. Estático: sin sudo, sin copiar el HOME, sin variables del
// navegador. El venv se construye en un directorio temporal (bajo `timeout`) y
// solo se mueve a su sitio si la versión instalada coincide con el pin, de modo
// que un fallo o un corte a medias nunca deja un venv roto en lugar del actual.
// Los scripts de bin/ se reescriben al destino porque un venv no es reubicable.
func memUInstallCommand() string {
	return `set -e
VENV="${XDG_DATA_HOME:-$HOME/.local/share}/workbench/venvs/memu"
NEW="$VENV.new-$$"
OLD="$VENV.old-$$"
mkdir -p "${VENV%/*}"
trap 'rm -rf "$NEW" "$OLD"' EXIT
timeout 600 python3 -m venv "$NEW"
timeout 600 "$NEW/bin/python" -m pip install --quiet --disable-pip-version-check 'memu-cli==` + memUVersion + `'
"$NEW/bin/python" -c "import importlib.metadata as m, sys; sys.exit(0 if m.version('memu-cli') == '` + memUVersion + `' else 1)"
"$NEW/bin/python" - "$NEW" "$VENV" <<'PYEOF'
import os
import sys
old, new = sys.argv[1], sys.argv[2]
bindir = os.path.join(old, "bin")
for name in os.listdir(bindir):
    full = os.path.join(bindir, name)
    if os.path.islink(full) or not os.path.isfile(full):
        continue
    with open(full, "rb") as f:
        data = f.read()
    if old.encode() not in data or b"\0" in data:
        continue
    mode = os.stat(full).st_mode
    with open(full, "wb") as f:
        f.write(data.replace(old.encode(), new.encode()))
    os.chmod(full, mode)
PYEOF
if [ -e "$VENV" ]; then mv "$VENV" "$OLD"; fi
mv "$NEW" "$VENV"
"$VENV/bin/python" -c "import importlib.metadata as m; print(m.version('memu-cli'))"`
}

// Bun release assets, SHA-256 from the official SHASUMS256.txt of bun-v1.4.2.
// The archive is verified before anything is extracted.
const (
	bunX64Asset   = "bun-linux-x64-baseline"
	bunX64SHA256  = "c678040f14fe0440eb839d37cbd0ce4c051a32da72806ac97de6a6aab6bf728f"
	bunARMAsset   = "bun-linux-aarch64"
	bunARMSHA256  = "54328bbc2d9c8e0c9f892c544d66c57a83b84139e34909e5ee81758f1ac8fda7"
	bunReleaseURL = "https://github.com/oven-sh/bun/releases/download/bun-v" + bunVersion + "/"
)

// bunInstallCommand instala el pin del ZIP oficial de oven-sh/bun en ~/.bun
// del usuario remoto. Elige el asset por `uname -m` (x86_64 o aarch64) y se
// niega en otras arquitecturas; descarga con timeout, verifica el SHA-256 del
// archivo, extrae a un temporal y renombra al destino. Una instalación que ya
// es ejecutable y responde a --version se conserva. Descarga y extracción con
// python3 (urllib/zipfile); el shell no recibe URLs ni comandos del navegador:
// todo el texto es estático. Sin sudo.
func bunInstallCommand() string {
	return `set -e
DEST="$HOME/.bun/bin/bun"
if [ ! -x "$DEST" ] || ! "$DEST" --version >/dev/null 2>&1; then
case "$(uname -m)" in
x86_64|amd64) ASSET=` + bunX64Asset + `; SHA=` + bunX64SHA256 + `;;
aarch64|arm64) ASSET=` + bunARMAsset + `; SHA=` + bunARMSHA256 + `;;
*) echo "arquitectura no soportada para bun" >&2; exit 1;;
esac
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
timeout 300 python3 - "$WORK" "$ASSET" "$SHA" <<'PYEOF'
import hashlib
import os
import sys
import urllib.request
import zipfile
work, asset, want = sys.argv[1], sys.argv[2], sys.argv[3]
url = "` + bunReleaseURL + `" + asset + ".zip"
archive = os.path.join(work, "bun.zip")
limit = 256 << 20
digest = hashlib.sha256()
total = 0
with urllib.request.urlopen(url, timeout=60) as response, open(archive, "wb") as out:
    while True:
        chunk = response.read(1 << 20)
        if not chunk:
            break
        total += len(chunk)
        if total > limit:
            sys.exit("archive too large")
        digest.update(chunk)
        out.write(chunk)
if digest.hexdigest() != want:
    sys.exit("checksum mismatch")
with zipfile.ZipFile(archive) as z:
    info = z.getinfo(asset + "/bun")
    if info.file_size > limit * 2:
        sys.exit("binary too large")
    data = z.read(info)
bindir = os.path.join(os.path.expanduser("~"), ".bun", "bin")
os.makedirs(bindir, exist_ok=True)
dest = os.path.join(bindir, "bun")
tmp = dest + ".tmp-" + str(os.getpid())
fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o755)
try:
    with os.fdopen(fd, "wb") as f:
        f.write(data)
        f.flush()
        os.fsync(f.fileno())
    os.replace(tmp, dest)
except BaseException:
    if os.path.exists(tmp):
        os.unlink(tmp)
    raise
PYEOF
fi
"$DEST" --version`
}

// parseDependencyProbe convierte la salida del sondeo en el orden canónico.
// Líneas `id|yes|`, `id|no|` e `id|version|valor`; lo ajeno se ignora y lo
// ausente se publica como no instalado, sin falso éxito.
func parseDependencyProbe(output string) []Dependency {
	present := map[string]bool{}
	versions := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(parts) != 3 {
			continue
		}
		id, kind, value := parts[0], parts[1], strings.TrimSpace(parts[2])
		if !slices.Contains(dependencyIDs, id) {
			continue
		}
		switch kind {
		case "yes":
			present[id] = true
			if value != "" {
				versions[id] = value
			}
		case "no":
			if _, ok := present[id]; !ok {
				present[id] = false
			}
		case "version":
			if value != "" {
				versions[id] = value
				present[id] = true
			}
		}
	}
	found := make([]Dependency, 0, len(dependencyIDs))
	for _, id := range dependencyIDs {
		row := Dependency{ID: id, Installed: present[id], Installable: installableDependency(id)}
		if row.Installed {
			row.Version = versions[id]
		} else if !row.Installable {
			row.Detail = "instalación no disponible desde Workbench"
		}
		found = append(found, row)
	}
	return found
}

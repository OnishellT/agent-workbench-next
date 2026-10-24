#!/bin/sh
# Consolida los avisos de los módulos Go enlazados en los binarios distribuidos, leídos del caché instalado sin red.
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
GO=${GO:-go}
# Sin red: un módulo que no esté en el caché falla en vez de descargarse.
GOPROXY=off
export GOPROXY
CACHE=$("$GO" env GOMODCACHE)
OUT=$ROOT/GO-NOTICES.txt
# El archivo válido no se toca hasta que la ejecución termine bien.
TMP=$OUT.tmp.$$
trap 'rm -f "$TMP"' EXIT INT TERM
HR='=============================================================================='

# Go escapa las mayúsculas del path del módulo en el caché: A -> !a.
escaped() { printf '%s' "$1" | sed 's/\([A-Z]\)/!\l\1/g'; }

notice_name() { grep -qiE '^(license|licence|copying|notice|copyright)' <<EOF
$1
EOF
}

# Textos de licencia/aviso en la raíz del módulo extraído, en orden estable.
notice_files() {
  find "$1" -maxdepth 1 -type f | LC_ALL=C sort | while IFS= read -r file; do
    if notice_name "$(basename "$file")"; then printf '%s\n' "$file"; fi
  done
}

emit_module() { # $1 = path, $2 = version
  dir="$CACHE/$(escaped "$1")@$2"
  [ -d "$dir" ] || { echo "not extracted in the module cache: $1@$2" >&2; exit 1; }
  files=$(notice_files "$dir")
  [ -n "$files" ] || { echo "no license text for $1@$2 in $dir" >&2; exit 1; }
  printf '\n%s\n%s@%s\n\n' "$HR" "$1" "$2" >>"$TMP"
  printf '%s\n' "$files" | while IFS= read -r file; do
    printf '%s\n%s\n' "$(basename "$file")" "$(cat "$file")" >>"$TMP"
  done
}

linked() { # $1 = module dir, $2 = package pattern
  listing=$(cd "$1" && "$GO" list -deps -tags nogui \
    -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}}{{end}}{{end}}' "$2") ||
    { echo "$GO list failed for $1 ($2)" >&2; exit 1; }
  [ -n "$(printf '%s' "$listing" | tr -d '[:space:]')" ] ||
    { echo "$GO list reported no module for $1 ($2)" >&2; exit 1; }
  printf '%s\n' "$listing" | sed '/^$/d' | LC_ALL=C sort -u
}

emit_binary() { # $1 = module dir, $2 = package pattern, $3 = label
  list=$(linked "$1" "$2")
  printf '\n################################################################################\n# %s — %s módulos\n' "$3" "$(printf '%s\n' "$list" | wc -l | tr -d ' ')" >>"$TMP"
  printf '%s\n' "$list" | while IFS= read -r entry; do
    emit_module "${entry%@*}" "${entry##*@}"
  done
}

{
  printf 'Agent Workbench — avisos de las dependencias Go enlazadas\n'
  printf 'Generado por scripts/go-notices.sh desde el caché de módulos instalado, sin red.\n'
  printf 'Módulos distintos del principal que `go list -deps -tags nogui` reporta para los dos\n'
  printf 'binarios que este release distribuye: ./cmd/workbench y third_party/magpie.\n'
  printf 'Cada módulo conserva su texto tal como lo publica; este archivo no interpreta licencias.\n'
} >"$TMP"

emit_binary "$ROOT" "./cmd/workbench" "./cmd/workbench (módulo workbench)"
emit_binary "$ROOT/third_party/magpie" "." "third_party/magpie (módulo github.com/yetone/magpie)"
mv "$TMP" "$OUT"
echo "wrote $OUT"

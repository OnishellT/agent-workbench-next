#!/usr/bin/env python3
"""Check that Docker packages Vite's output, Magpie's Bun runtime and the release notices."""

from pathlib import Path, PurePosixPath
import os
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
dockerfile = (ROOT / "Dockerfile").read_text()
vite_config = (ROOT / "web/vite.config.ts").read_text()

web_stage = re.search(
    r"(?ms)^FROM oven/bun:1\.4\.2 AS web-build\n(?P<body>.*?)(?=^FROM )",
    dockerfile,
)
go_stage = re.search(
    r"(?ms)^FROM golang:[^\n]+ AS binary-build\n(?P<body>.*?)(?=^FROM )",
    dockerfile,
)
if not web_stage or not go_stage:
    raise SystemExit("Dockerfile is missing the Bun or Go build stage")


def one(pattern: str, text: str, label: str) -> str:
    matches = re.findall(pattern, text, re.MULTILINE)
    if len(matches) != 1:
        raise SystemExit(f"expected one {label}, found {len(matches)}")
    return matches[0]


web_workdir = one(r"^WORKDIR (\S+)$", web_stage["body"], "web WORKDIR")
# El aviso servido se compone en este build: sus entradas deben estar copiadas antes.
notice_copy = web_stage["body"].find("COPY NOTICE.md GO-NOTICES.txt /src/")
if notice_copy == -1 or notice_copy > web_stage["body"].index("RUN bun run build"):
    raise SystemExit("web-build stage must copy NOTICE.md and GO-NOTICES.txt into /src/ before `bun run build`")
print("PASS the frontend build receives NOTICE.md and GO-NOTICES.txt")

go_workdir = one(r"^WORKDIR (\S+)$", go_stage["body"], "Go WORKDIR")
out_dir = one(r"\boutDir\s*:\s*[\"']([^\"']+)[\"']", vite_config, "Vite build.outDir")
if re.search(r"\broot\s*:", vite_config):
    raise SystemExit("Vite root is explicit; update this check for its configured root")

copy_source, copy_target = one(
    r"^COPY\s+--from=web-build\s+(\S+)\s+(\S+)\s*$",
    go_stage["body"],
    "frontend COPY",
)
expected_source = PurePosixPath(web_workdir) / out_dir
actual_source = PurePosixPath(copy_source)
actual_target = PurePosixPath(go_workdir) / copy_target
expected_target = PurePosixPath(go_workdir) / "web" / out_dir
if actual_source != expected_source or actual_target != expected_target:
    raise SystemExit(
        f"Vite output mismatch: expected {expected_source} -> {expected_target}, "
        f"got {actual_source} -> {actual_target}"
    )
print(f"PASS Vite output {expected_source} copied to Go embed path {expected_target}")
runtime = dockerfile.split(" AS runtime\n", 1)[1]
if (
    "COPY --from=web-build /usr/local/bin/bun /usr/local/bin/bun" not in runtime
    or "MAGPIE_BUN=/usr/local/bin/bun" not in runtime
):
    raise SystemExit("runtime must copy the pinned Bun and configure native MAGPIE_BUN")
print("PASS native Magpie Bun is included and configured in the runtime stage")
# El archivo servido viaja en el binario; aquí se comprueba la receta y sus fuentes.
packaged = ("NOTICE.md", "GO-NOTICES.txt", "internal/foundry/LICENSE", "third_party/magpie/LICENSE")
for path in packaged:
    if not (ROOT / path).is_file():
        raise SystemExit(f"{path} is missing from the build context")
if "COPY NOTICE.md GO-NOTICES.txt /usr/share/doc/agent-workbench/" not in runtime:
    raise SystemExit("runtime must package NOTICE.md and GO-NOTICES.txt")
renamed = {
    "internal/foundry/LICENSE": "LICENSE-AGPL-3.0-only.txt",
    "third_party/magpie/LICENSE": "LICENSE-magpie-MIT.txt",
}
for source, target in renamed.items():
    if f"COPY {source} /usr/share/doc/agent-workbench/{target}" not in runtime:
        raise SystemExit(f"runtime must package {source} as /usr/share/doc/agent-workbench/{target}")
print("PASS release notices and license texts are packaged in the runtime stage")
# GO-NOTICES.txt se contrasta con los módulos que el compilador enlaza, sin red.
go = os.environ.get("GO", "go")
template = "{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}}{{end}}{{end}}"
notices = (ROOT / "GO-NOTICES.txt").read_text()
for module_dir, pattern in ((".", "./cmd/workbench"), ("third_party/magpie", ".")):
    listing = subprocess.run(
        [go, "list", "-deps", "-tags", "nogui", "-f", template, pattern],
        cwd=ROOT / module_dir,
        env={**os.environ, "GOPROXY": "off"},
        capture_output=True,
        text=True,
    )
    if listing.returncode != 0:
        raise SystemExit(f"cannot list linked modules of {module_dir}: {listing.stderr.strip()}")
    linked = {line for line in listing.stdout.split() if line}
    missing = sorted(m for m in linked if f"\n{m}\n" not in notices)
    if missing:
        raise SystemExit(f"GO-NOTICES.txt does not cover {module_dir}: {', '.join(missing)}")
print("PASS GO-NOTICES.txt covers every module linked into both binaries")
# Sin este paso la imagen usa VERSION=development y no identifica ningún release.
if not re.search(r'^ARG VERSION=\S+$', dockerfile, re.MULTILINE) or 'make binaries VERSION="${VERSION}"' not in dockerfile:
    raise SystemExit("binary-build stage must pass the release VERSION into make binaries")
print("PASS release version is passed into the image build")
# Magpie es un proyecto con su propia identidad: su binario no hereda el VERSION del release.
makefile = (ROOT / "Makefile").read_text()
magpie_build = one(r"^.*cd third_party/magpie.*$", makefile, "Magpie build line")
magpie_pin = one(r"^- Incorporated release: v(\S+)$", (ROOT / "third_party/magpie/WORKBENCH.md").read_text(), "Magpie release pin")
# The pin may be a Makefile variable (MAGPIE_VERSION ?= x.y.z); resolve it to its default.
variable = re.search(r"^MAGPIE_VERSION\s*\??=\s*(\S+)$", makefile, re.MULTILINE)
if variable:
    magpie_build = magpie_build.replace("$(MAGPIE_VERSION)", variable.group(1))
if "$(VERSION)" in magpie_build or f"-X main.version={magpie_pin}" not in magpie_build:
    raise SystemExit(f"Magpie build must keep its documented pin {magpie_pin}, not Workbench's VERSION")
print(f"PASS Magpie keeps its documented version {magpie_pin}")

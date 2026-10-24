# 0006 — Toolchain via a Nix flake dev shell

Status: accepted (2026-10-21)

## Context
`go` and `bun` are not on the global PATH on the development machines; the user manages tooling with Nix and nix-direnv (`~/nix-config`). The repository pins Go 1.26.8 (`go.mod`, Dockerfile) and bun 1.4.2 (`web/package.json`).

## Decision
- `flake.nix` provides a `devShell` with `go`, `bun`, `python3`, `gnumake`, `git` from the same nixpkgs revision as `~/nix-config` (`8edc0c72…`), which ships exactly those versions, with `CGO_ENABLED=0` and `GOTOOLCHAIN=local` preset.
- `.envrc` mirrors nix-config (`use flake`, project-local `TMPDIR`); `.direnv/` is git-ignored.
- Verification runs as `nix develop -c <command>` so it works with or without direnv.

## Consequences
- Bumping Go/bun means bumping both the flake input and the Dockerfile/go.mod/package.json pins together.
- Flakes see only git-tracked files; new files must be at least `git add -N` before `nix develop` notices them.

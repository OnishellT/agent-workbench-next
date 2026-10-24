# 0001 — Single Go binary (net/http + SQLite) with embedded React frontend

Status: accepted (observed in code)

## Context
Workbench replaced an earlier Electron/Node-style stack ("MIGRATION_PLAN.md"). The product must run on Linux devices and inside a minimal Docker image, and be copied to remote hosts over SSH as a helper.

## Decision
- One Go module `workbench` built static (`CGO_ENABLED=0`, `-tags nogui`, `-trimpath`) for linux/amd64.
- Standard library HTTP mux and handlers; no web framework, DI or ORM.
- `modernc.org/sqlite` (pure Go) with JSON payload tables per module and in-code migrations.
- The React/Vite frontend is compiled to `web/dist` and embedded via `web/embed.go`.

## Consequences
- Builds need no C toolchain; the same binary serves locally and as the host helper.
- `go test ./...` must list packages explicitly to avoid GOROOT copies under `work/`.
- Frontend changes require `cd web && bun run build` before Go tests of `./web/...` see them.

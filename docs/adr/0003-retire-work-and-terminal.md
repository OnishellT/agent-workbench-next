# 0003 — Retire Work and the built-in terminal

Status: accepted (plan.md; user confirmed 2026-10-21)

## Context
The earlier product mounted a "Work" module and a web terminal with a private `agents/home`. That HOME never represented the real Linux user and conflicted with per-host isolation.

## Decision
- Work and terminal are removed from UI, API and startup. `internal/app` answers 404 for `/api/work`, `/api/terminal` and `/api/usage/overview`; `cmd/workbench/main.go` and `internal/hostagent/run.go` no longer wire them.
- The user's external SSH terminal replaces the built-in one; work distribution happens through native agents and Sim remote actions.
- `internal/work`, `internal/terminal`, `internal/usageoverview` and `web/src/modules/{work,terminal,usageoverview}` were deleted on 2026-10-21 together with the `@xterm` dependencies. They must never be re-wired.
- `foundry.EnsureWorkSource` keeps the canonical history of Work as evidence and is not removed.

## Consequences
- Tests expecting Work must be adapted, not satisfied by resurrecting the module.
- Connections absorbs the devices list; the shell shows exactly seven sections.

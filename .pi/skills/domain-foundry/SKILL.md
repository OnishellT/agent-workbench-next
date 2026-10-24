---
name: domain-foundry
description: Conversations, Tasks and the Foundry section — capture/normalization of agent histories (Go), the embedded Python Conversation Foundry served at /foundry/, and the dormant memU pipeline. Use when touching internal/foundry, internal/foundryloop, third_party/foundry, internal/memu, web/src/modules/conversations or foundry.
---

# Foundry: Conversations, Tasks, Foundry section

## Two things called Foundry
- **`internal/foundry` (Go)**: Conversations and Tasks sections; its learning/memU engine has no UI since 2026-10-23 (ADR 0008).
- **`third_party/foundry` (Python)**: the standalone Conversation Foundry app that *is* the Foundry section. `internal/foundryloop` runs it on demand on loopback and proxies it at `/foundry/` behind the session (token injected server-side); `web/src/modules/foundry/FoundrySection.tsx` frames it and drives its page by `postMessage`. Pin, patches and overrides: `third_party/foundry/WORKBENCH.md`. Rebuild its UI with `make foundry-ui`, test with `make foundry-test`.
- Invariants: Foundry's token never reaches the browser; it keeps its own store/human gates; capture stays opt-in (no auto-registered sources); the dashboard must open when Python/Foundry is missing.


## Vocabulary
- **Source**: enabled conversation directory (local or imported from a host) with project/host/agent attribution. Supported readers: Codex, Claude, Pi, OMP and the canonical format — not universal.
- **Session / revision / event**: a captured conversation, its stable revision and normalized events (`store.go` tables `sessions`, `revisions`, `events`).
- **Task**: work extracted from conversations with **citations** and human decisions (label, completion, feedback) kept in `task_*_history` tables.
- **Proposal**: candidate skill / verifier script / AGENTS.md rule derived from tasks; evaluated (positive + negative control) and **adopted explicitly** with diff and rollback (`learning_*.go`, `proposals.go`).
- **memU**: external CLI (`memu-cli==0.11.0b3`) that prepares jobs from a private export of enabled sources; the user's native agent processes them; results are proposals, not adoptions (`memu.go`, `memu_commit.go`, `memu_synthesis.go`, `internal/memu/artifacts.go`).
- **Provider**: the user's already-configured model provider used by the processor (`providers.go`, `provider_process*.go`); missing config is shown as a real pending item.
- **Work source**: `EnsureWorkSource` keeps the canonical history of the retired Work; it is historical evidence — do not delete.

## Key files
- `internal/foundry/module.go` — routes `/api/conversations`, `/api/tasks`, `/api/foundry/*` (memu, proposals, backup).
- `internal/foundry/sources.go`, `sessions.go`, `normalize.go`, `tasks.go`, `host_history.go` (import side of the hosts contract), `automation.go` (automates proposals, not SSH capture).
- `web/src/modules/conversations/*`, `web/src/modules/foundry/*`.
- Docs: `docs/memu-replacement.md`. Tests: `CGO_ENABLED=0 GOAMD64=v1 go test -tags nogui ./internal/foundry`.

## Invariants
- Human decisions (labels, completions, adoption) are never overwritten by automation.
- Citations must stay stable across re-capture; re-capture must not duplicate sessions.
- memU reads only the private export directory, never the user's HOME; memory/embedding credentials stay out of Git and the dashboard.
- Prepare/process ≠ adopt. Adoption installs only the approved artifact, preserves foreign rules, and must be reversible.
- General skills need evidence from several domains; never fabricate generality or savings numbers; never label this engagement in real data to pass a test.

## Pitfalls
- Store migrations live in `store.go` (`schema_info`); add migrations, don't edit existing `CREATE TABLE`s.
- `internal/foundry` package name does not imply keeping the old learning engine; do not extend it beyond memU integration.
- Revocation in flight (source, identity, app close) must cancel capture and processing and never publish results of a revoked authorization.

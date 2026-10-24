# AGENTS.md — Workbench

Dashboard for projects and AI agents on Linux devices. Go backend (net/http +
SQLite) serving an embedded React/TypeScript frontend; Magpie and Sim are
vendored upstream apps run alongside. **`plan.md` is the only source of scope,
work split and acceptance (V1–V8).** Read it by section, then only the code,
callers and local instructions of your task. Start with narrow searches and
widen only to resolve a concrete doubt.

## Stack and toolchain

- Go 1.26.8 (`go.mod`, module `workbench`), `modernc.org/sqlite` (pure Go,
  `CGO_ENABLED=0`), `golang.org/x/crypto` SSH, `github.com/pkg/sftp`, websockets.
- Frontend: React 19 + Vite + Tailwind in `web/`, built with bun 1.4.2, embedded
  via `web/embed.go` (needs `web/dist`; a checked-in build exists).
- Toolchain comes from the Nix flake: `nix develop` or direnv (`.envrc`,
  `use flake`). The pin is the same nixpkgs as `~/nix-config` (go 1.26.8, bun 1.4.2).
- Release and CI builds use the latest Go 1.26 patch (`go.mod` only sets the minimum, 1.26.8): the
  stdlib advisories fixed in 1.26.9/1.26.10 (`govulncheck` in CI) are not in the pinned dev shell yet
  (recorded in `docs/adr/0006-nix-flake-dev-shell.md`, amendment 2026-10-24).
  Bump the flake pin when nixpkgs ships a patched Go.
- `third_party/magpie` is a separate Go module; `third_party/sim` has its own
  bun workspace and Dockerfiles. Pins and licences: `NOTICE.md`, `third_party/magpie/WORKBENCH.md`.

## Commands

```sh
nix develop                                   # or let direnv load .envrc
go vet  -tags nogui ./cmd/... ./internal/... ./web/...
GOAMD64=v1 go test -tags nogui ./cmd/... ./internal/... ./web/...
make check        # frontend build + Go tests + asset check + typecheck + vet
make binaries     # dist/workbench and dist/magpie (static linux/amd64)
cd web && bun run typecheck && bun test tests/unit
cd web && bun run test:e2e   # Playwright; needs E2E_BASE_URL + E2E_ACCESS_TOKEN_FILE of a trial instance
./dist/workbench --state-dir /abs/trial --listen 127.0.0.1:PORT   # flags: internal/config/config.go
```

Always pass `-tags nogui` and the explicit package list (Magpie is a separate
module). A fresh clone needs `cd web && bun install --frozen-lockfile && bun run build`
first: `web/embed.go` embeds `web/dist`. Focal tests first; full suite when integrating.

## Architecture map

| Path | Role |
| --- | --- |
| `cmd/workbench/main.go` | Wires config, modules, native sites, HTTP servers; `WORKBENCH_REMOTE_ACTOR_ONLY` disables local Magpie (Docker). |
| `internal/app` | Single-origin server: token login → random in-memory session cookie, CSP, Origin checks, drain/close, `/api/bootstrap`, `/api/settings`, `/api/diagnostics`, inference proxy, dispatch to modules, native Magpie sites per host. |
| `internal/config` | Flags (`--state-dir --listen --public-url --sim-url --token-file --trusted-proxy`), state paths, lock. |
| `internal/hosts` | Connections: host registry (SQLite), SSH identity/trust, dependency detection/install, host helper agent upload/proxy, automatic SSH conversation capture, WireGuard diagnostic. |
| `internal/foundry` | Conversations/Tasks: sources, normalization, revisions, citations, tasks, proposals, providers; memU export/prepare/process is dormant (no UI). |
| `internal/foundryloop`, `third_party/foundry` | Conversation Foundry (Python, vendored, embedded in the binary): started on demand on loopback and proxied at `/foundry/` behind the session; `WORKBENCH_FOUNDRY_PYTHON`, `WORKBENCH_FOUNDRY_DATA`. |
| `internal/memu` | Serves memU artifact files under authorization. |
| `internal/models`, `internal/harness` | Model gateway, native agent profiles and harness bundle. |
| `internal/settings`, `internal/hostagent` | Settings store; the remote host helper binary entry. |
| `web/src/app` | Shell: navigation, i18n, theme, Connections, Settings, palette. |
| `web/src/modules/{hosts,models,conversations,foundry,sim}` | Section UIs by domain (Work/terminal/usage overview were deleted; their APIs answer 404). |
| `web/tests/e2e` | Playwright release specs per domain. |
| `compose.yaml`, `Dockerfile`, `deploy/Caddyfile` | Workbench + Sim + Postgres/pgvector + Redis + realtime + embeddings; `--profile vps` adds the Caddy TLS proxy on the host network. |
| `scripts/backup.sh` | Dumps Sim's PostgreSQL and tars the data volumes; `.env` is copied separately. |
| `adapters/pi` | Pi extension: `workbench_api` tool + `/workbench` command over the HTTP API (`pi install ./adapters/pi`; `bun test` there). |

Sections that must remain exactly: Conversations, Tasks, Foundry, Sim,
Agent configuration (Magpie), Connections (incl. devices), Settings.

## Conventions

- Keep the current style and structure: stdlib HTTP mux patterns, SQLite JSON
  payload tables, module handlers injected through `app.Modules`.
- One agent by default. With an explicit request for subagents, the coordinator
  uses the assignments/paths in `plan.md` §4 and keeps a single writer per
  shared file (`cmd/**`, `internal/app/**`, config/settings/hostagent/harness,
  `web/src/app/**`, Makefile/Docker/manifests, hosts `module.go server.go hosts.go hosts_public.go host_agent_proxy.go`).
- Cross-domain contract A/B: `hosts.HistoryImporter` (`internal/hosts/host_history.go`)
  implemented by `foundry.Module.ImportHostConversation`; change it only via the coordinator.
- Technical references on demand only: `docs/operations.md`, `docs/network.md`,
  `docs/research.md`, `docs/memu-replacement.md`. Histories, other checkouts,
  reports, `work/` and conversations are not product instructions.
- Tests may expect retired features: adjust them per `plan.md`; keep tests of
  live features; never resurrect Work to turn them green.
- Normal outputs and the total of `functions.exec`: up to 2000 tokens. Keep long
  logs out of the active context; show only useful evidence and diagnosis.

## Hard rules

- MUST preserve existing changes and data. The tree has staged, unstaged and
  new files that are part of the base: no reset, mass cleanup, staging or
  commits of someone else's work.
- MUST keep authentication, SSH identity verification, per-host isolation
  (dedicated origins, cookies, sessions) and Foundry's human decisions when
  changing code.
- MUST keep the user's chosen model and default effort. Foundry/meta-harness
  flows, captures, evaluations and benchmarks only when the user asks or they
  are the task; never run them routinely.
- NEVER put credentials in Git, logs or delegated prompts. Native config is
  verified under the authorised user with a trial profile; never copy or chmod
  the real HOME.
- NEVER bring back Work, the built-in terminal or a usage overview (deleted; the
  external SSH terminal and Magpie replace them). `/api/work|terminal|usage/overview` stay 404.
- NEVER accept arbitrary shell from the browser: installs from Connections are
  limited to known dependencies with per-host target and result.
- NEVER capture beyond enabled sources; never point memU at the whole HOME.
- NEVER create alternative Magpie/Sim editors, SSO, a workflow engine,
  scheduler, microservices, DI/ORM frameworks, an updater, or another usage
  tracker (usage belongs to Magpie).
- NEVER replace pending acceptance items with mocks, 501, skips or screenshots.

## CI and review

- Pull requests run `.github/workflows/ci.yml` (required check `ci-ok`) and `security.yml`
  (`security-ok`); `make check` is a subset (gofmt, vet, staticcheck, Go/web/Foundry tests, typecheck, packaging guard; not the tooling tests, image smoke test or browser specs). `main` is protected (`.github/rulesets/main.json`).
- Merging needs the local reviewer: `make review PR=<n>` (pi, read-only, confidence 1-5) on the final
  commit with confidence 4 or 5 and no actionable findings; `review-gate` enforces it. The reviewer's
  rules are read from the base branch (`.github/review.json`, `.github/copilot-instructions.md`,
  `.github/instructions/`, `scripts/review/`). After three rounds without convergence, stop and ask.
- Keep the committed Foundry UI bundle in sync (`make foundry-ui`); CI fails on a stale one.

## Skills and decisions

- `.pi/skills/domain-hosts-magpie/SKILL.md` — Connections, SSH, host helper, Magpie panels.
- `.pi/skills/domain-foundry/SKILL.md` — Conversations, Tasks, the Foundry section (Python app), memU backend.
- `.pi/skills/domain-sim/SKILL.md` — Sim integration and API routes.
- `.pi/skills/domain-app-shell/SKILL.md` — Auth, native sites, dispatch, frontend shell.
- `docs/adr/` — architecture decisions (toolchain, upstream apps, retired Work, memU, native sites).

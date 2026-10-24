---
name: domain-app-shell
description: Workbench application shell — process wiring, token/cookie auth, Origin/CSP checks, API dispatch, native Magpie sites, settings/diagnostics, and the React shell (navigation, i18n, theme, Connections, Settings). Use when touching cmd/workbench, internal/app, internal/config, internal/settings or web/src/app.
---

# App shell, auth and native sites

## Vocabulary
- **Access token**: generated into `--token-file` under `--state-dir`; `POST /api/auth` checks it and sets an instance-specific cookie (`instanceCookieName`) holding a random in-memory session (`sessions`, 30-day TTL). The token itself is never a valid cookie value. `/api/logout` revokes the session; a restart drops all sessions. Inference proxy paths (`/v1/*`, `/backend-api/codex/*`) require the Bearer token instead.
- **Bootstrap**: `GET /api/bootstrap` returns sections, `sim_url`, `ssh_agent` (SSH_AUTH_SOCK visible to the process), settings and module availability for the frontend.
- **Modules**: `app.Modules` struct injected by `cmd/workbench/main.go` (Models, AgentModels, NativeSites, Foundry, Hosts, Settings, ModuleDiagnostics). A nil module answers `501 <name>_not_implemented`; retired `/api/work`, `/api/terminal`, `/api/usage/overview` answer 404.
- **Native site**: `app.NativeSite{HostID, Listen, Origin, Handler}`; one per host, served on its own listener/origin with grants and sessions (`native_sites.go`) so cookies never cross hosts.
- **Drain/close**: requests are tracked; `Close` cancels active work and waits for drain (`close_test.go`). Shutdown must leave no orphans.
- **State dir**: private; holds SQLite DBs, token, lock (`internal/config/lock.go`), Magpie runtime files.

## Key files
- `cmd/workbench/main.go` — single integration point; `WORKBENCH_REMOTE_ACTOR_ONLY`, `WORKBENCH_PUBLIC_URL`, `WORKBENCH_MAGPIE_ORIGIN`.
- `internal/app/app.go` (`dispatchAPI`, Origin/upgrade checks, CSP in `csp_test.go`), `native_sites.go`, `agent_gateway.go`, `settings.go`.
- `internal/config/config.go` flags and `normalizeListen`/`NormalizePublicURL`; `--trusted-proxy` for reverse proxies.
- `web/src/app/*`: `App.tsx`, `navigation.ts` (the seven sections), `i18n.ts`/`language.ts`, `theme.ts`, `Connections.tsx`, `Settings.tsx`, `SectionBoundary.tsx` (per-section error isolation), `api.ts`.
- `web/embed.go` embeds `web/dist`; `scripts/check_docker_web_output.py` validates the build output.

## Invariants
- Single origin for the dashboard; every mutating/WebSocket request needs a matching `Origin`.
- Only seven sections: Conversations, Tasks, Foundry, Sim, Agent configuration, Connections (with devices), Settings. Desktop and mobile layouts, language and theme must keep working.
- Loopback listener by default (`127.0.0.1:18888`); remote access goes through a proxy/TLS reaching `--public-url`.
- A failing Sim or host must not block the dashboard (`SectionBoundary`, health/diagnostics).

## Pitfalls
- Shared files (`cmd/**`, `internal/app/**`, config/settings/hostagent/harness, `web/src/app/**`, Makefile/Docker/manifests) are coordinator-only during parallel work.
- The Docker image runs UID 10001 with `HOME=/data`; it must not start a local Magpie with the container identity.
- Keep `-tags nogui` everywhere; the GUI build tags of Magpie are not used.

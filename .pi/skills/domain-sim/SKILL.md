---
name: domain-sim
description: Sim workflow app integration — upstream pin, compose services, API routes used by agents, and the Sim section UI. Use when touching web/src/modules/sim, compose.yaml Sim services or third_party/sim.
---

# Sim integration

## Vocabulary
- **Sim**: upstream `simstudioai/sim` v0.9.25 (`8df65ad3`), Apache-2.0, vendored in `third_party/sim` with its own bun workspace and Dockerfiles. Runs as independent services (app, realtime, migrations, embeddings) with PostgreSQL/pgvector and Redis in `compose.yaml`.
- **Sim origin**: `--sim-url` / `WORKBENCH_SIM_URL` / `SIM_PUBLIC_URL`, normalized in `internal/config` and published as `bootstrap.sim_url`.
- **Workflow**: created/edited/deployed/executed by agents through Sim's official API: `/api/v2/workflows`, then `{id}/state`, `{id}/deploy`, `{id}/execute`. API keys go in `X-API-Key`; Bearer is OAuth. Some writes need a personal key/OAuth, not a workspace key. An edited draft is not deployed.

## Key files
- `web/src/modules/sim/SimSection.tsx` — embeds the original UI in an iframe at the Sim origin.
- `internal/config/config.go` (`normalizeSIMURL`), `internal/config/sim_url_test.go`.
- `compose.yaml` services with `context: third_party/sim`; `web/tests/e2e/sim-release.spec.ts` (when present) run from `web` with `bun run test:e2e sim-release.spec.ts`.

## Invariants
- Reuse upstream routes of the incorporated pin; consult the pinned endpoint before using it. No alternative editor, workflow engine or scheduler in Workbench.
- Sim blocks do not execute on another host without a configured remote tool/action; the VPN only provides connectivity.
- Sim down must not prevent opening the dashboard. Do not rotate Sim's encryption key blindly; volumes `sim_postgres`, `sim_uploads` hold state.
- Patches to `third_party/sim/**` only when integration demands it; keep its build, licences (`LICENSE`/`NOTICE` copied into images) and separate updatability.

## Pitfalls
- An iframe or a manually created graph does not satisfy V3; the acceptance path is API/MCP create → deploy → execute from distinct clients with persistence after restart.
- Never ship secrets to the JS bundle; API keys are agent-side.
- Sim uses `/socket.io/` to realtime; proxies must pass it through.

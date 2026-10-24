# 0002 — Magpie and Sim are vendored upstream apps, built and updated separately

Status: accepted (code + NOTICE.md)

## Context
Agent configuration and workflow editing already exist as mature open-source apps. Rewriting them inside Workbench would duplicate work and lose updatability.

## Decision
- `third_party/magpie` (MIT, v0.1.1265) is a separate Go module built by `make binaries` into `dist/magpie` with `nogui`; Workbench serves its original UI through per-host native sites.
- `third_party/sim` (Apache-2.0, v0.9.25) keeps its bun workspace and official Dockerfiles; `compose.yaml` runs it with its own PostgreSQL/pgvector, Redis, realtime and embeddings services.
- Local patches are limited to integration (native-panel framing, backup delivery) and documented in `third_party/magpie/WORKBENCH.md` and `NOTICE.md`.
- No alternative editors, SSO, workflow engine, scheduler or self-made updater.

## Consequences
- Each app is updated by replacing its pin and re-reviewing the patch diff; the root Go test suite does not cover them.
- Token/usage tracking stays in Magpie; Workbench never adds a second tracker.

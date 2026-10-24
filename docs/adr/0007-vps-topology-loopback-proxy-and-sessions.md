# 0007 — VPS topology: loopback-only listeners, host-network TLS proxy, in-memory sessions

Status: accepted (2026-10-21, audit follow-up)

## Context
Workbench binds only to loopback and trusts `X-Forwarded-Proto` only from loopback proxies (`internal/config`). The session cookie used to carry the master access token, so a leaked cookie was unrevocable. Per-host Magpie panels need one origin each.

## Decision
- Keep the loopback-only rule. The TLS proxy runs on the host network: `caddy` service under `--profile vps` with `deploy/Caddyfile` routing `WORKBENCH_DOMAIN` → 18888, `*.WORKBENCH_DOMAIN` → 18889 (wildcard certificate via DNS-01), `SIM_DOMAIN` → 3000 and `/socket.io/` → 3002.
- `/api/auth` issues a random 32-byte session kept in memory (30-day TTL, 32 entries); the cookie never holds the token and logout revokes server-side. A restart requires the token again.
- `/api/bootstrap` publishes `ssh_agent` so the UI hides the SSH-agent mode where the process has no agent (Docker, services).
- Release binaries stay amd64 by default (`GOARCH` in Makefile); pulled images drop `platform:` pins.
- `scripts/backup.sh` is the documented full-state backup; `.env` is copied separately.

## Consequences
- A proxy in a bridge network or another machine cannot front Workbench without changing `loopbackOnlyPrefix`.
- Sessions do not survive restarts or multiple replicas (one operator, one process assumed).

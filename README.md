# Agent Workbench

[![CI](https://github.com/OnishellT/agent-workbench-next/actions/workflows/ci.yml/badge.svg)](https://github.com/OnishellT/agent-workbench-next/actions/workflows/ci.yml)
[![Security](https://github.com/OnishellT/agent-workbench-next/actions/workflows/security.yml/badge.svg)](https://github.com/OnishellT/agent-workbench-next/actions/workflows/security.yml)

A dashboard for projects and AI agents on Linux devices: one Go binary (net/http + SQLite) serving an
embedded React/TypeScript frontend. Sections: **Conversations**, **Tasks**, **Foundry** (conversation
learning loop), **Sim** (workflow app), **Agent configuration** (Magpie), **Connections** (hosts, SSH,
VPN) and **Settings**. English and Spanish, light and dark.

Magpie and Sim are vendored upstream apps run alongside; Foundry is a vendored Python app the binary
embeds and starts on demand. Pins and licences: [NOTICE.md](NOTICE.md).

## Run it

**Docker** (server or VPS):

```sh
cp .env.example .env     # fill in the secrets it lists
docker compose up -d     # add --profile vps for the Caddy TLS proxy
```

**Binary** (Linux amd64; Python 3.11+ on PATH is needed only for the Foundry section):

```sh
(cd web && bun install --frozen-lockfile && bun run build)   # the binary embeds web/dist
make binaries                                                  # dist/workbench and dist/magpie
./dist/workbench --state-dir "$HOME/.local/state/agent-workbench-next" --listen 127.0.0.1:18888
```

The access token is created in the state directory (`access.token`). Operations, backups and the VPS
topology: [docs/operations.md](docs/operations.md).

## Develop

`nix develop` provides the pinned toolchain. `make check` runs what CI runs. Architecture and the rules
agents and people follow: [AGENTS.md](AGENTS.md); scope: [plan.md](plan.md); how to contribute and the
review gate: [CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities as described in
[SECURITY.md](SECURITY.md).

Technical references, only when a task needs them: [network lab notes](docs/network.md),
[primary sources](docs/research.md), [Foundry replacement notes](docs/memu-replacement.md).

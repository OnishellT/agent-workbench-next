---
name: domain-hosts-magpie
description: Connections section — host registry, SSH identity/trust, dependency install, host helper agent, automatic conversation capture and per-host native Magpie panels. Use when touching internal/hosts, internal/hostagent, web/src/modules/hosts or models.
---

# Hosts, Connections and Magpie

## Vocabulary
- **Host**: a registered Linux device (`hosts.Host`, SQLite `hosts` table, secret blob per host). The local PC is host `local`; `dev@10.0.0.50` is the second lab Linux (IPs change: verify).
- **Trust**: SSH host-key fingerprint verified by the user (`POST /api/hosts/{id}/trust`). Password SSH works; key auth with `BatchMode=yes` was rejected in the lab — never assume key auth.
- **Host helper / host agent**: `dist/workbench` + `dist/magpie` uploaded over SFTP and run under the host's Linux user (`internal/hosts/host_agent.go`, `internal/hostagent/run.go`). Install lock is per uid-verified directory; stale locks older than the install timeout are reaped.
- **Native site**: a dedicated origin per host (`<label>.magpie.localhost` or a loopback port) serving that host's Magpie UI through `host_agent_proxy.go`; opened by `POST /api/hosts/{id}/magpie/open`.
- **Dependencies**: known packages detected/installed per host (`dependencies.go`); never arbitrary shell.
- **Source**: a per-host conversation directory enabled for capture (`hosts.Source`); capture is paginated (up to 8 files + cursor) over SSH (`conversations_ssh.go`).
- **HistoryImporter**: contract with Foundry (`host_history.go`), implemented by `foundry.Module.ImportHostConversation`, wired with `SetHistoryImporter`.

## Key files
- `internal/hosts/module.go`, `server.go`, `hosts.go`, `hosts_public.go`, `host_agent_proxy.go` — shared files, coordinator-only.
- `internal/hosts/ssh.go`, `ssh_command.go`, `magpie_ssh.go`, `memu_runner.go`, `wireguard.go` (`GET /api/network/wireguard`).
- `cmd/workbench/main.go` `remoteNativeSite` — origin resolution; `WORKBENCH_REMOTE_ACTOR_ONLY=true` (Docker) disables the local Magpie actor.
- `web/src/modules/hosts/*`, `web/src/modules/models/*`, `web/src/app/Connections.tsx`.
- Tests: `CGO_ENABLED=0 GOAMD64=v1 go test -tags nogui ./internal/hosts ./internal/models`.

## Invariants
- Magpie configures the agent/profile the user actually opens on that host; a private HOME (old Work, container) is not the Linux user. From Docker, access goes to the host user over SSH.
- Public status responses exclude token, owner nonce, uid, pid, binary hash, ports (`hostAgentStatus` comment).
- An unreachable host shows an error; no local fallback. Another host must not change.
- Capture only enabled sources; cancel on source revocation, identity change or app close; no duplicates on re-capture.
- Keep upstream Magpie pin v0.1.1265 and its local patches (`third_party/magpie/WORKBENCH.md`); build with `nogui`.

## Pitfalls
- Cookies are not isolated by port: native sites need distinct hostnames or the authenticated-session dance in `internal/app/native_sites.go`.
- Remote capture was historically manual (`POST /api/hosts/{id}/history/sync`); automatic drain of pages is the V4 target — don't reintroduce a "Sync" dependency.
- Never store the SSH password; use a temporary trial key with verified fingerprint and remove it afterwards.

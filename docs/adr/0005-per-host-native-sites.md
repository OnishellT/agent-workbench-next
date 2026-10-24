# 0005 — One native origin per host for Magpie panels

Status: accepted (observed in internal/app/native_sites.go, cmd/workbench/main.go)

## Context
Magpie's UI uses absolute API paths and cannot be mounted under a prefix. Browsers do not isolate cookies by port, and each host's Magpie must act as that host's Linux user.

## Decision
- Each host gets a dedicated `NativeSite` with its own listener and origin (`<label>.magpie.localhost` or a loopback port, `WORKBENCH_MAGPIE_ORIGIN` as base) proxied through the host helper.
- The dashboard grants a short-lived session to the native site (`POST /api/hosts/{id}/magpie/open`); Host/Origin headers, gateway auth and plugin-page permissions of upstream Magpie are preserved.
- In Docker (`WORKBENCH_REMOTE_ACTOR_ONLY=true`) no local Magpie is started with the container identity; even the local machine is reached over SSH as a registered host.

## Consequences
- Remote access needs a proxy/TLS and hostnames that reach those origins; `127.0.0.1` on another machine is not the server.
- Showing a page never installs an agent or configures another user; an unreachable host shows an error without local fallback.

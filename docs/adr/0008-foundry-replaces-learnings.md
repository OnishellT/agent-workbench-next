# 0008 — Foundry replaces the Learnings section

Status: accepted (user decision 2026-10-23). Supersedes the Learnings UI of 0004.

## Context
Learnings had been reduced to a single memU panel. The user asked to replace it completely with the standalone Conversation Foundry (a Python app with its own UI, store and evaluation loop) and to integrate it into Workbench.

## Decision
- Vendor Foundry in `third_party/foundry` (pin and local patches in its `WORKBENCH.md`) and embed its runtime in the Workbench binary.
- Workbench supervises it as a loopback child process started on demand (`internal/foundryloop`) and serves it at `/foundry/` behind the dashboard session; the section "Foundry" frames it. It needs Python 3.11+ on the Workbench machine and says so when it is missing; the dashboard never depends on it.
- Foundry keeps its own store (`<state-dir>/foundry-loop/data`), token, provider settings and human gates (task completion, correctness, adoption). Workbench does not copy, mirror or re-decide any of them.
- The old `?section=learnings` links open Foundry's learning page. The Tasks section no longer creates evaluation cases; it points to Foundry's Experiments page.

## Consequences
- The memU UI is gone. The memU backend (`internal/foundry/memu*.go`, `internal/memu`, the `memu` dependency in Connections, the image's memU venv) stays dormant until a separate decision removes it.
- `internal/foundry` still owns Conversations and Tasks; Foundry (Python) captures from its own sources, so a source enabled in Conversations is not captured by Foundry until it is added in Foundry's Sources page.
- Foundry follows Workbench's language selector (Spanish/English) live; its own dictionary lives in `third_party/foundry/ui/src/i18n/`.
- Foundry's `command` providers and verifiers execute commands the user configures in Foundry, as upstream does. This is not the browser-shell the Connections rule forbids, but it is reachable only through the Workbench session.

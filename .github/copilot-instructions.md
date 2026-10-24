# Review instructions

Repository-wide rules for reviewers (people, the local reviewer and GitHub Copilot). The binding
source is [AGENTS.md](../AGENTS.md); this file is the short list a reviewer checks every diff against.

## Blocking (critical)

- Credentials in code, logs, tests, prompts or fixtures. The access token and provider keys never leave the state directory.
- A way around the session cookie, the Origin/Host checks, SSH host-identity verification, or per-host isolation (dedicated origins, cookies and sessions per native Magpie site).
- Arbitrary shell reachable from the browser. Installs from Connections are limited to known dependencies with a per-host target and result.
- Capture of conversations beyond the sources the user enabled; memU or Foundry pointed at a whole HOME.
- Foundry's human decisions (task completion, correctness, adoption) taken by automation, or an adoption that is not reversible.
- A retired feature coming back: Work, the built-in terminal, the usage overview. `/api/work`, `/api/terminal` and `/api/usage/overview` keep answering 404.
- Alternative editors for Magpie or Sim, SSO, a workflow engine or scheduler, another usage tracker.

## Must not drift without a stated decision

- HTTP API shapes and route names (the Pi adapter and the web client depend on them).
- SQLite schema: add a migration, never edit an existing `CREATE TABLE`.
- User-facing text and i18n keys; error text that tests and the UI match on.
- The `hosts.HistoryImporter` contract between `internal/hosts` and `internal/foundry`.
- The user's chosen model and default reasoning effort.

## Quality

- Failure paths handled and tested; contexts cancelled on shutdown or revocation; no lock held across I/O.
- Tests that can fail: negative and edge cases for what the change adds.
- Reuse an existing helper before writing a second one; no speculative options or layers.
- Documentation changed with the behaviour (`docs/`, `AGENTS.md`, the ADRs).

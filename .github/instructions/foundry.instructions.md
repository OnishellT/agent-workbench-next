---
applyTo: "third_party/foundry/**,internal/foundry/**,internal/foundryloop/**,internal/memu/**,web/src/modules/foundry/**,web/src/modules/conversations/**"
---
# Foundry

- `third_party/foundry` is a vendored app with local patches listed in its `WORKBENCH.md`. Keep patches small; never touch `foundry_loop/vendor/**` (sha256 provenance).
- `foundry_loop/web` is the committed build of `ui/`: rebuild it (`make foundry-ui`) whenever `ui/src` changes. CI fails on a stale bundle.
- Every UI string and backend message needs an English entry (`ui/src/i18n/en.ui.ts`, `en.server.ts`); strings assembled at runtime need a `{placeholder}` pattern.
- `internal/foundryloop` starts the Python child lazily on loopback and injects its token server-side; the token must not reach the browser or logs, and `WORKBENCH_*` variables are not passed to the child.
- Capture is opt-in; a fresh store registers no sources on its own. Human decisions are never overwritten by automation.

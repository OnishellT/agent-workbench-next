# 0004 — memU replaces the in-house learning processor

Status: accepted (user decision 2026-10-18, docs/memu-replacement.md); its Learnings UI was replaced by Foundry in [0008](0008-foundry-replaces-learnings.md)

## Context
Turning captured conversations into reusable skills, verifier scripts and AGENTS.md rules needed a processing engine. memU, Acontext, Hermes and Hindsight were compared against their official sources.

## Decision
- Use the original memU CLI, pinned `memu-cli==0.11.0b3`, as an independent dependency; do not extend the Foundry engine.
- Workbench exports only conversations from enabled sources into a private processing directory; memU never reads the whole HOME.
- The user's native agent, with the model already selected by the user, processes the prepared jobs; memU stores and retrieves skills.
- Results are proposals shown in Learnings. Explicit review, citations and per-project scope precede any installation; prepare/process is not adoption.
- Missing memory/embedding configuration is surfaced as a real pending item, never mocked.

## Consequences
- Capture, normalization, revisions, citations and existing data remain in `internal/foundry`; the package name does not imply keeping the old engine.
- Backend credentials for memU live outside Git and are never displayed in the dashboard.

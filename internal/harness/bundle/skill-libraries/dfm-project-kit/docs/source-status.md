# DFM documentation source status

The machine-readable record is `.atl/source-revisions.json`.

## Verified revisions

| Source | Revision used on 2026-08-19 | Role |
| --- | --- | --- |
| `main` | `bd954b995323700ce3c6c51de565d6328f2f2b67` | Current code, tests, configuration, and review instructions. |
| `docs-atl-skills-cleanup` | `02ee61cdeecdf27ca49d66803a08a3b078556f65` before this refresh | Existing skills, catalogs, contracts, payload registry, and setup docs. |
| `doc/dfm-repo-handbook` | `8593dfa0b18fe2e2ff80fbdb8e136a908da86123` | Historical architecture, operations, debugging, and glossary material. |

No Lambda repository was used for this verification.

## Authority

Use current main or the active PR head for mechanics. Use Jira and approved decision records for intended behavior. Use ATL docs and the handbook to find context and source paths. Confirm their claims against current code and tests.

## Material drift found

Main contains substantial work that postdates both documentation branches:

- SAM NextGen physical, rental, auto-sale, refund, fulfillment, pickup, and tax paths;
- event-scoped NextGen outbox publication and replay;
- correlated physical refund lifecycle records;
- per-store pickup location cache;
- order-processing lifecycle events, failure persistence, dashboards, alerts, cleanup, and recovery isolation;
- stronger fulfillment claim isolation and COESP retry handling;
- current dependency, Python, Django, Ruff, CI, and Make behavior.

The handbook does not describe all of this. Older ATL summaries cover part of the NextGen evolution but stop before the current main revision.

## Refresh procedure

1. Record the new main SHA and active documentation branch SHAs.
2. Compare code, configuration, tests, management commands, and recent first-parent history.
3. Update the short docs first.
4. Update domain skills and the skill registry to point to current sections.
5. Keep old historical material, but label it when it no longer describes current behavior.
6. Run the agent-doc validator.

Do not update documentation by copying commit titles alone. Verify the shipped code and tests behind each claim.

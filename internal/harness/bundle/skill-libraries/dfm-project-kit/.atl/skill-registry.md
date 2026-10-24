# DFM skill registry

Use one workflow skill and the smallest matching domain skill. The workflow controls the result. The domain skill controls what code and documents to inspect.

## Workflow skills

All workflow and domain skills live in `skills/<name>/SKILL.md`.

| Skill | Use when |
| --- | --- |
| `dfm-context-research` | Reconstruct why a business or technical decision was made. |
| `dfm-flow-trace` | Explain one current DFM code path end to end from a pinned revision. |
| `dfm-decision-capture` | Turn verified discussion into a draft decision record. |
| `dfm-work-map` | Map a multi-session initiative into decisions, vertical Jira tickets, blockers, and one current frontier. |
| `dfm-delegation` | Run a bounded parallel batch for independent DFM slices while the parent keeps integration and mutations. |
| `dfm-blast-radius` | Trace hidden consumers and prove the safety facts behind a DFM change. |
| `dfm-incident-investigation` | Diagnose a concrete DFM symptom without changing state. |
| `dfm-pr-review` | Review a PR against Jira, business rules, code, tests, comments, and CI. |
| `dfm-pr-remediation` | Classify and fix selected PR findings locally, test them, and draft replies without publishing. |
| `dfm-git-pr-sync` | Analyze or perform an authorized branch sync, resolve conflicts by intent, and verify the result. |
| `dfm-pr-delivery` | Prepare or publish verified local work as commits and a reviewable PR, then inspect initial CI. |
| `dfm-implementation-plan` | Design one scoped ticket's technical implementation before editing. |
| `dfm-plan-verify` | Audit every approved plan promise against the working tree, tests, drift, and current authority. |
| `dfm-tdd` | Run one red, green, clean behavior slice through a correct DFM test seam. |
| `dfm-ticket-implementation` | Implement a scoped Jira story or subtask locally. |
| `dfm-qa-validation` | Plan or execute QA, or generate bounded synthetic test data. |
| `dfm-local-development` | Set up, run, test, or diagnose the local DFM development environment. |
| `dfm-production-access` | Inspect approved production access or ECS state read-only after an explicit production request. |

If the requested workflow is not available, do not route it to the closest skill. Use `dfm-git-pr-sync` for rebase or branch synchronization and `dfm-pr-delivery` for commit, push, or PR creation. `dfm-work-map` may publish only its approved map, subtasks, and links. Generic Jira writes, merge, release, deployment, production mutation, and admin operations still require dedicated workflows.

## Project domain skills

Every supported agent runtime uses the plugin's canonical `skills/<name>/SKILL.md` directory. Do not create `.agents` or `.claude` mirrors.

Domain skills marked `disable-model-invocation` are reference packs. The selected workflow reads them explicitly; they should not replace the workflow.

| Skill | Domain | Start with |
| --- | --- | --- |
| `dfm-legacy-sam` | Legacy XML, ECEDOC, classification, retries, refunds, and pickup | `docs/workers-and-messages.md`, `docs/contracts/sam-legacy-ecedoc.md` |
| `dfm-sam-nextgen` | NextGen digital, physical, rental, refund, outbox, and notifications | `docs/architecture.md`, `docs/data-and-state.md` |
| `dfm-shopify-fulfillment` | Shopify orders, fulfillments, fees, refunds, and webhooks | `.github/instructions/shopify-review.instructions.md` |
| `dfm-coesp-smart-license` | COESP and Smart License provisioning, errors, retries, and redaction | `.github/instructions/coesp-review.instructions.md`, `.github/instructions/smart-license-review.instructions.md` |

## Reading rules

1. Read `docs/README.md`.
2. Choose one short document.
3. Search for the relevant heading or term.
4. Read that section and its cited paths.
5. Open a contract, payload registry, or the pinned handbook only when needed.

The active code and tests outrank branch documentation for current mechanics.

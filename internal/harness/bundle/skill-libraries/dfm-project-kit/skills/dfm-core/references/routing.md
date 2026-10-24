# DFM routing

Select one primary skill from the requested outcome. URLs and identifiers are supporting objects, not routing decisions.

| Requested outcome | Primary skill |
|---|---|
| Reconstruct why a business or technical decision was made | `dfm-context-research` |
| Explain how a current DFM code path works end to end | `dfm-flow-trace` |
| Turn verified discussion into a draft decision record | `dfm-decision-capture` |
| Map a multi-session DFM initiative and its Jira frontier | `dfm-work-map` |
| Diagnose unexpected runtime or business behavior | `dfm-incident-investigation` |
| Judge a PR or branch against its ticket and invariants | `dfm-pr-review` |
| Resolve verified PR findings or local CI failures | `dfm-pr-remediation` |
| Find what a change could break beyond the diff | `dfm-blast-radius` |
| Implement a Jira story or subtask locally | `dfm-ticket-implementation` |
| Design the technical implementation for one scoped ticket | `dfm-implementation-plan` |
| Run one test-first DFM behavior slice | `dfm-tdd` |
| Audit implemented work against an approved plan | `dfm-plan-verify` |
| Synchronize a PR branch or resolve conflicts | `dfm-git-pr-sync` |
| Commit, push, or open verified work as a PR | `dfm-pr-delivery` |
| Plan or execute QA, or generate bounded test data | `dfm-qa-validation` |
| Set up, run, test, or diagnose the local development environment | `dfm-local-development` |
| Inspect approved production access or ECS state read-only | `dfm-production-access` |

`dfm-delegation` is a support skill. Keep the primary workflow. Read delegation too when the user asks for parallel work or the scoped workflow has at least two independent slices.
Do not route unsupported outcomes to the nearest skill. `dfm-work-map` may perform only its approved Jira map, subtask, and link operations. Generic Jira mutation, merge, release tags, deployment, production mutation, and admin operations require dedicated workflows that are not in the current library.

## Ambiguity

Proceed when the outcome is clear and missing details are discoverable. Ask one focused question only when the answer changes the primary skill, environment, target, or mutation class.

Examples:

- PR + "review" -> `dfm-pr-review`.
- PR + "test in stage" -> `dfm-qa-validation`.
- Jira + "implement" -> `dfm-ticket-implementation`.
- Order + "why did this fail?" -> `dfm-incident-investigation`.
- Slack thread + "what was decided?" -> `dfm-context-research`.
- Slack thread + "record this decision" -> `dfm-decision-capture`.
- Current code + "how does this flow work?" -> `dfm-flow-trace`.
- Jira + "create the implementation plan" -> `dfm-implementation-plan`.
- Local setup, Make target, or test-environment diagnosis -> `dfm-local-development`.
- Explicit read-only production access or ECS inspection -> `dfm-production-access`.

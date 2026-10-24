# DFM Mutation Contract

## Classes

| Class | Examples | Authorization |
|---|---|---|
| M0 read-only | Read Jira, PRs, code, logs, documents, browser state | Proceed |
| M1 local reversible | Edit a worktree, add tests, run local commands | Allowed only when implementation was requested |
| M2 remote development write | Push branch, create/update PR, create non-deploying tag | Exact target and operation must be explicit |
| M3 external state or communication | Jira edits/transitions, PR/Slack comments, resend/reprocess | Draft by default; execute only when explicitly requested |
| M4 privileged, production, destructive | Deploy, production data mutation, secret rotation, merge, delete, force push | Preview and just-in-time confirmation required |

"Fix the ticket" authorizes M1. It does not authorize M2 through M4.

## Memory writes

Recall is M0. Explicit `retain`, `memory_edit update`, and `memory_edit invalidate` are persistent local M1 actions. A code-edit request alone does not authorize them; user-selected automatic retention or an explicit request to remember does.

Read the complete `memory://<id>` record before changing it. `memory_edit` applies only to editable memories; fact-table rows are read-only. `memory_edit forget` and `/memory clear` are destructive: show the exact editable memory IDs or scoped banks and obtain just-in-time confirmation.

## Before an external write

1. Re-read the target and version: issue update time, PR head SHA, branch tip, page identity, or resource version.
2. State system, account/tenant, environment, target IDs, exact operation, expected change, and reversibility.
3. Show the material payload or diff without secrets.
4. Confirm authorization still matches the current target.

## Execute and verify

- Execute once. Retry only when the operation is proven idempotent or the prior result is known.
- Fetch after-state and compare it with the expected change.
- Report each operation independently. Never claim rollback or atomicity without evidence.
- Preserve old branch tips before history rewriting. Use force-with-lease where rewriting is explicitly authorized; never use force.

## Browser writes

Immediately before submit, verify canonical URL, environment, tenant/account, and target. A prior screenshot is not authorization for a current click.

## Stop conditions

Stop rather than guess when the target changed, environment is unclear, permissions are broader than expected, a partial write occurred, or a production/destructive action lacks final confirmation.

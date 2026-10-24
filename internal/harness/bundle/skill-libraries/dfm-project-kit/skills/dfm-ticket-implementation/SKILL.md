---
name: dfm-ticket-implementation
description: "Use to implement a clearly scoped DFM Jira story or subtask in a local worktree, including focused tests and local verification. Do not use for investigation-only work, PR review, review-comment remediation, push, PR creation, Jira mutation, merge, tag, or deployment."
---

# DFM Ticket Implementation

Turn accepted requirements and business invariants into the smallest complete local change. Do not publish it from this skill.

Read [DFM core](../dfm-core/SKILL.md) and the matching domain skill first. Open [repository sources](../dfm-core/references/repository-sources.md) only when historical documentation or another checkout matters; [decision context](../dfm-core/references/context.md) when accepted rules have temporal or scoped meaning; [trust and data](../dfm-core/references/trust-and-data.md) before handling external or private material; [mutations](../dfm-core/references/mutations.md) before the first edit; [evidence](../dfm-core/references/evidence.md) before final claims; and [receipt](../dfm-core/references/receipt.md) only before final output.

## Establish the contract

1. Retrieve the governing ticket, parent, dependencies, acceptance criteria, explicit exclusions, and applicable accepted decisions/business rules.
2. Pin base revision, branch/worktree state, and repository instructions.
3. Select the matching domain skill, then verify its paths and behavior against the active code and tests.
4. Discover real build and test commands from the active `Makefile` and project configuration. Prefer repository Make targets; do not hardcode historical commands from this skill.
5. Convert requirements into observable contracts. If a material behavior remains ambiguous, stop before editing and return the precise question.

## Test-first option

If the user requests TDD, or the contract has a cheap seam that benefits from a red-first proof, read `dfm-tdd`. Keep `dfm-ticket-implementation` as the owning workflow.

## Implement

1. Inspect existing patterns and domain structures before introducing another convention.
2. Locate definitions and all references before changing exported or shared symbols.
3. Plan small, coherent work units that each end in a verifiable state.
4. Fix the source behavior. Do not suppress warnings, special-case one incident payload, or preserve obsolete paths unless required.
5. Preserve applicable DFM contracts for idempotency, retries/DLQ, tenant isolation, identifiers, payload shape, state transitions, and sensitive-field redaction by following the active domain instructions.
6. Keep tests with the behavior they protect. Add a test only when the observable contract is not already defended.
7. Verify each work unit before starting the next.
8. Run focused tests through the current repository targets, then the smallest meaningful wider check.
9. Exercise the changed path directly when a runtime surface is available.
10. Review the final diff for accidental scope, stale comments, compatibility shims, secrets, and generated noise.
11. If the diff changes a shared model, message, state transition, identifier, or integration contract, read `dfm-blast-radius` before handoff.

## Output contract

Return one DFM receipt as the final response. Include the requirement-to-change result, files and symbols changed, focused and direct verification, unresolved risk, local worktree state, and only the next capability the evidence requires. Put a long change matrix or command output in one artifact and link it when created. Do not add a separate implementation summary.

M1 is authorized only because implementation was requested. Do not push, create/update a PR, edit Jira, publish comments, merge, tag, deploy, or mutate an external environment. At a long analysis-to-edit boundary, hand off the normalized contract into a fresh session.

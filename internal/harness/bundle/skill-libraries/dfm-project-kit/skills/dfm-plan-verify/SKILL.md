---
name: dfm-plan-verify
description: Use after a DFM implementation followed an approved plan. Compare every promised step, assumption, file, symbol, deletion, migration, and verification check with the current working tree and ticket. Report implemented, partial, missing, diverged, drift, and a final read-only verdict.
---

# DFM plan verification

Audit the implementation against the approved plan. Do not reconstruct the plan from conversation memory.

Read `dfm-core`, the governing Jira ticket, relevant decision records, and the selected domain skill first.

## Locate and pin the plan

Use this order:

1. User-provided plan path or `local://` URI.
2. The approved plan reference in the current session context.
3. The current session's local files when one plan clearly matches the repository and ticket.
4. A named cross-session plan only after verifying project, ticket, date, and title.

If several plans could apply, ask for the exact path. Do not audit the newest file by filename alone.

Record plan path, content hash when available, repository, ticket, base SHA, current HEAD, and plan approval state.

A plan records what the implementation promised. Current Jira and accepted decisions still govern intended behavior. Report any conflict between them.

## Build the audit checklist

Extract one item for every concrete promise:

- file, model, migration, or configuration change;
- symbol added, changed, renamed, moved, or removed;
- signature, field, status, literal, payload, or error contract;
- caller migration or legacy-path deletion;
- test or runtime verification;
- documentation or operational change;
- assumption and its chosen fallback.

Keep the plan's order and identifiers. Split a step only when it contains independently checkable promises.

## Verify implementation evidence

Assign one status per item:

- `IMPLEMENTED`: every stated claim matches current evidence;
- `PARTIAL`: some claims match and the missing claims are named;
- `MISSING`: no implementation evidence exists;
- `DIVERGED`: work exists but differs from the plan;
- `SUPERSEDED`: current approved scope replaced the planned step.

Every status needs current evidence:

- read the exact file and symbol;
- use language-aware references for renames, removals, and shared interfaces;
- search exact literals, fields, and stale names;
- inspect migrations, settings, tests, messages, and docs reached by the step;
- use the base-to-head diff to map changed files and detect unplanned work.

Plausibility and a matching filename are not evidence.

## Run verification

Run each local, authorized command from the plan with its stated working directory and prerequisites. Record expected and observed results.

Use these statuses:

- `PASS`
- `FAIL`
- `NOT_RUNNABLE`
- `NOT_AUTHORIZED`

Do not execute browser, queue, admin, production, external-write, or destructive checks from a read-only audit. Name the exact manual or authorized check needed.

A failure is evidence. Do not fix and rerun during this skill.

## Detect drift

List changed files and behavior not owned by a plan item:

- `BENIGN`: generated output or mechanical fallout required by a planned step;
- `UNPLANNED`: new behavior or scope absent from the plan;
- `UNRELATED`: local work that belongs elsewhere.

Do not call unplanned behavior benign because tests pass.

## Delegation

For a plan with many independent items, read `dfm-delegation`. Split read-only evidence collection by disjoint item groups. The parent verifies shared contracts, runs all commands once, detects drift, and owns the verdict.

## Verdict

- `PASS`: every item is implemented or validly superseded, every runnable check passes, and no unplanned drift remains.
- `PASS_WITH_UNVERIFIED`: implementation matches, but one or more required checks are not runnable or authorized.
- `FAIL`: any item is partial, missing, wrongly diverged, any required check fails, or unplanned behavior remains.

## Output contract

Write the complete item, verification, and drift tables to one `local://` artifact.

Return one DFM receipt as the final response. Include the verdict, failed or unverified plan items, authority conflicts, residual risk, smallest action for each gap, and artifact URI. Do not copy the tables into chat.

Remain M0. Do not edit files, fix gaps, stage, commit, push, update Jira, publish comments, or mutate an environment.

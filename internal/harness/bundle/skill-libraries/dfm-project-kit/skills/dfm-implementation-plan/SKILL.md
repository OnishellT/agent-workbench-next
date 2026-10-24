---
name: dfm-implementation-plan
description: "Use to design the technical implementation for one scoped DFM Jira story or subtask before editing. Produce one pinned, executable plan that maps accepted requirements to current code, contracts, work units, and verification."
---

# DFM implementation plan

Design one scoped change before implementation. Produce a plan another fresh session can execute and `dfm-plan-verify` can audit. Remain read-only and do not turn planning into implementation.

Read [DFM core](../dfm-core/SKILL.md) and the matching domain skill first. Open [repository sources](../dfm-core/references/repository-sources.md) only when historical documentation or another checkout matters; [decision context](../dfm-core/references/context.md) when accepted rules have temporal or scoped meaning; [evidence](../dfm-core/references/evidence.md) before finalizing plan claims; [trust and data](../dfm-core/references/trust-and-data.md) before handling external or private material; and [receipt](../dfm-core/references/receipt.md) only before final output.

## Planning gate

Use this workflow when the requested behavior is accepted and the technical change is substantial enough to benefit from a durable plan.

Do not create the plan when:

- the initiative spans several dependent decisions, tickets, or PRs; use `dfm-work-map`;
- intended behavior is unresolved or authoritative sources conflict; use `dfm-context-research`, then `dfm-decision-capture` when a real decision is ready;
- the request is a small mechanical change whose files, behavior, and verification are already obvious; route it directly to the owning workflow;
- the user asked to implement now and no separate plan artifact is required; `dfm-ticket-implementation` may plan small coherent work units internally.

State which gate applies. Do not manufacture design work to justify a plan.

## Pin the planning snapshot

Record:

- repository, checkout, branch, base revision, HEAD, and worktree state;
- Jira story or subtask, parent, dependencies, acceptance criteria, exclusions, and observed update time;
- accepted BDRs, business rules, repository instructions, and selected domain skill;
- relevant runtime, deployment, compatibility, and authorization constraints;
- exact plan artifact identity and creation time.

A volatile ticket, PR, branch, or decision must be re-read before the final plan. If its governing generation changes, invalidate affected analysis rather than merging old and new evidence.

## Normalize the contract

1. Convert every accepted requirement and applicable invariant into stable IDs: `R1`, `R2`, ...
2. Express each ID as observable input, state transition, output, side effect, or failure behavior.
3. Separate required behavior, explicit exclusions, assumptions, and unresolved blockers.
4. Stop when a blocker changes the business contract, environment, target, or mutation class. Do not bury it as an implementation assumption.

## Trace the current implementation

Use language-aware definitions, references, callers, registrations, tests, and current repository instructions. Trace only the paths needed to design the change.

Identify:

- entrypoints and dispatch boundaries;
- existing domain types, services, helpers, and extension seams to reuse;
- persisted models, constraints, transactions, identifiers, and state transitions;
- messages, queues, topics, outbox records, retries, DLQs, and idempotency boundaries;
- Shopify, SAM, COESP, HTTP, admin, or other external contracts reached by the change;
- current tests and the narrowest public seams that can disprove the new behavior.

Current code proves mechanics, not intended policy. Historical docs may explain a shape but do not override the pinned code or accepted contract.

## Choose the design

Prefer the existing DFM pattern that satisfies the normalized contract. Before introducing a helper, dependency, abstraction, or parallel path, check current code, Python/Django facilities, platform capabilities, and installed dependencies.

Compare at most two viable designs, and only when a real tradeoff exists. For each option, name the difference in behavior, maintenance, migration, operational risk, and verification. Choose one design. Do not hand implementation a menu of unresolved alternatives.

The chosen design must define:

- changed contracts and preserved invariants;
- files and symbols to add, change, move, or delete;
- data migration, backfill, compatibility, rollout, and rollback behavior when applicable;
- error, retry, partial-success, and observability behavior;
- obsolete paths to remove after every caller is migrated;
- security, tenant, privacy, and authorization boundaries reached.

Read `dfm-blast-radius` when a shared model, identifier, payload, message, state transition, integration contract, or operational action changes. Carry proven and unproven safety facts into the plan.

## Build executable work units

Each work unit must end in a coherent, verifiable state and contain:

1. requirement IDs owned;
2. exact files and symbols;
3. behavior to add or change;
4. contracts and non-goals to preserve;
5. deletion or migration required for a clean cutover;
6. focused check that fails if the unit is wrong;
7. prerequisite or ordering constraint.

Keep sequential units in order. Mark independent read-only research or disjoint implementation ownership explicitly; use `dfm-delegation` only when its independence gate passes. Shared models, migrations, interfaces, and state transitions keep one owner.

## Verification design

Define the smallest set of checks that covers the contract:

- existing focused tests to run first;
- one red-first seam when `dfm-tdd` is warranted;
- new observable-contract test only where coverage is missing;
- static checks and repository Make targets;
- direct runtime, API, worker, or browser scenario when available and authorized;
- QA follow-up, environment, data, evidence, and cleanup requirements;
- checks that remain unavailable or require a separate workflow.

Never put a production replay, deploy, Jira transition, push, or other unauthorized mutation into a local verification command.

## Durable plan

Store the approved plan as a session-local `local://` artifact by default so planning does not dirty the repository. Use a repository path only when the user explicitly requests a tracked plan.

The plan must include:

- snapshot and authority;
- goal and non-goals;
- requirement/invariant matrix;
- current implementation anchors;
- chosen design and rejected alternative when one was materially evaluated;
- ordered work units;
- data, compatibility, rollout, and rollback sections when applicable;
- blast-radius facts and risks;
- verification matrix;
- blockers and authorization boundaries.

Do not reconstruct this artifact from conversation memory later. `dfm-ticket-implementation` consumes the pinned plan; `dfm-plan-verify` audits the same artifact after implementation.

## Output contract

The plan artifact is the detailed output. Keep the requirement matrix, design evidence, work units, verification commands, blast-radius facts, and authorization boundaries there.

Return one DFM receipt as the final response. Include the planning gate result, plan URI or path, chosen design, work-unit count and ordering, blockers, and `dfm-ticket-implementation` as the next capability. Do not copy the plan or its matrices into chat.

Remain M0 unless the user explicitly authorized a tracked plan file. Do not edit source or tests, create branches, commit, push, create or update a PR, mutate Jira, execute QA writes, or change an environment.
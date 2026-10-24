---
name: dfm-delegation
description: Use when the user asks to parallelize a DFM task or when a scoped task has at least two independent slices. Launch one bounded batch, keep mutations and final verification in the parent, and skip delegation for sequential or stateful work.
---

# DFM delegation

Delegate to reduce elapsed time. Do not delegate to make a small task look busy.

Read `dfm-core` first. The parent owns the user's intent, shared contract, authorization, integration, and final result.

## Delegation gate

Delegate only when every answer below is yes:

1. Are there at least two useful slices?
2. Can each slice finish without another slice's result?
3. Does each slice have a clear target and acceptance check?
4. Can each slice avoid external mutations?
5. Will parallel work save time after integration cost?

If one answer is no, keep the work in the parent and continue. Do not ask the user to approve an obvious no-delegation decision.

## Parent contract

Before spawning agents, pin:

- ticket, PR, branch, head SHA, environment, and domain as applicable;
- the shared goal and non-goals;
- interfaces or files owned by each slice;
- the evidence and output format;
- mutation limits;
- the verification the parent will run.

Pass shared information once in the task batch context. Give each agent only its slice. Do not paste the full conversation, Jira body, PR diff, logs, or unrelated documentation.

## Agent selection

Use the narrowest agent type that fits:

- `scout` for read-only code, Jira, docs, history, and source mapping;
- `reviewer` for code or diff review;
- `security-reviewer` for auth, secrets, permissions, sensitive data, or dependency risk;
- `librarian` for external library and API source research;
- `designer` for visual design work;
- `sonic` for mechanical collection or edits with no judgment;
- a general worker for one independent implementation unit when no specialist fits.

Do not use several agents for the same question unless the user asks for competing answers or an adversarial review.

## Batch rules

Spawn all independent slices in one `task` call. Do not serialize work that can start together.

Every task must state:

```text
# Target
Exact files, symbols, sources, and non-goals.

# Change
The work to perform and the contract to preserve.

# Acceptance
The evidence or local result the agent must return.
```

Tell subagents to skip formatting, lint, builds, and tests. They may identify the focused checks their slice needs. The parent runs verification once after integration.

Subagents must return findings, file and line references, IDs, commands run, and unresolved risks. They should reference large output by artifact instead of pasting it.

## Boundaries

Do not delegate:

- browser actions in an authenticated environment;
- Jira, Slack, GitHub, admin, release, deployment, or production writes;
- secret access or credential entry;
- merge, push, rebase, tag, replay, resend, or reprocess;
- one investigation whose next step depends on the previous result;
- an ambiguous task that the parent has not scoped;
- recursive delegation.

A subagent may research a proposed mutation. The parent must re-read the target, request any required authorization, execute the mutation, and verify after-state.

## DFM patterns

For a PR review, split requirements and decision evidence from code and test review. Add a security reviewer only when the diff touches a security boundary.

For an incident, reproduce and pin identifiers first. Then split independent system boundaries such as SAM persistence and Shopify or COESP processing. The parent owns the timeline and root-cause decision.

For implementation, delegate only independent work units with separate ownership. Shared models, migrations, interfaces, and state transitions need one owner.

For QA, a subagent may draft scenarios or inspect existing coverage. The parent runs browser, queue, admin, and environment actions.

## Parent finish

Keep working while agents run. Do not poll. When results arrive:

1. Check each result against its acceptance contract.
2. Resolve contradictions against current evidence.
3. Integrate only supported findings or changes.
4. Run the final focused verification.
5. Return one DFM receipt as the final response. Link detailed child evidence by artifact and do not paste or summarize each child report separately.

Cancel work that became stale after a changed PR head, ticket scope, branch, or environment.

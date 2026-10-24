# DFM Decision Context

Keep these records distinct:

- Slack, Jira, documents, PRs, code, tests, and runtime state are source evidence.
- A Business Decision Record (BDR) explains what was decided, why, scope, provenance, and supersession.
- A Business Rule (BR) states the behavior that is currently expected.
- An index or memory is a rebuildable retrieval aid, never authority.
- A Context Packet is the small task-specific subset given to a workflow.

## Retrieval order

1. Search accepted BDRs, active BRs, and the glossary for the named entities and time scope.
2. Check current Jira and pinned code/PR state.
3. Search linked documents and complete Slack threads when rationale or missing history matters.
4. Include runtime or observability evidence only when the question concerns actual behavior.

Do not search whole workspaces without anchors. Start with ticket keys, PRs, entities, flows, participants, dates, and likely channels or documents.

## Mnemopi retrieval

Use recalled memory to propose search anchors, never as direct evidence. A recalled claim remains unverified until the current authority confirms it. Do not cite a memory in a Context Packet when the underlying source can be cited.

Good durable memories are verified repository conventions, stable source locations, resolved debugging lessons, and user workflow preferences. Include provenance and time scope when staleness matters.

Do not retain credentials, customer data, private Slack text, production output, transient branch or PR state, current incident hypotheses, or copies of complete BDRs and BRs. Store a stable source pointer instead. When memory conflicts with current authority, preserve the authority and read the complete record. Update or invalidate it only when its store is editable; Mnemopi fact rows are read-only and must be reported as stale rather than claimed as changed.

With Mnemopi `per-project` scoping, the bank identity includes the absolute working-directory path. Different clones and worktrees do not imply shared recall.

## Decision states

`candidate → proposed → accepted → superseded`, with `rejected` and `deprecated` as terminal alternatives.

An agent may draft `candidate` or `proposed`. It must not mark a decision `accepted` without explicit human/domain approval or an already authoritative approved source.

## Context Packet

Keep the packet under roughly 1,200 tokens by default. Include:

- query, time, scope, and source freshness;
- active BRs;
- relevant accepted BDRs and short rationale;
- current Jira/PR facts;
- superseded context only when it explains history;
- contradictions, unknowns, and citations.

Reference source IDs and links. Read a full BDR or source only when the task hinges on its detail.

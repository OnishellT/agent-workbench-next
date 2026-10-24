---
name: dfm-work-map
description: Use when a DFM initiative is too large for one session, spans several decisions or delivery tickets, or needs Jira subtasks and blocking links. Build a low-resolution map, expose only the current frontier, keep each ticket within one fresh session, and write Jira only after an exact preview and authorization.
disable-model-invocation: true
---

# DFM work map

Map a large initiative before it becomes one endless agent session. The map holds links and current state. Decisions and ticket details live in their own records.

Read `dfm-core`, the governing Jira story or epic, current decision records, and the relevant domain skills first. Read `dfm-context-research` only when the initiative depends on unresolved rationale or incomplete decision history.

## Select a mode

- `CHART`: define the destination, decision frontier, and draft ticket graph. M0.
- `PUBLISH`: create or update the authorized Jira map, subtasks, and links from an approved draft. M3.
- `ADVANCE`: resolve one frontier decision or prepare one delivery ticket for a fresh session. M0 by default.

Do not mix charting and implementation. One session charts the map or advances one item.

## Name the destination

The destination is the observable end state of the initiative. Derive it from the parent Jira item, accepted decisions, current code, and explicit user constraints.

Keep it to two lines. If the sources conflict, record the conflict as a decision item. Ask one focused question only when the destination cannot be established from available authority.

## Map shape

Use the existing Jira epic or user story as the map when it already owns the effort. Do not create a second parent without explicit need.

The map contains:

```markdown
## Destination

<observable end state>

## Standing context

<domain skills, source revisions, constraints, and mutation limits>

## Decisions so far

- `Decision title` | `Jira URL` | one-line result

## Frontier

<open, unblocked, unclaimed items found from Jira links, not copied bodies>

## Not yet specified

<in-scope questions that cannot yet be stated as tickets>

## Out of scope

<explicit boundaries and their source>
```

The map is an index. Do not copy full ticket bodies, Slack threads, plans, or BDRs into it.

## Item types

Use four types:

- `DECISION`: a business or technical choice. Resolve with evidence and a draft BDR when the decision meets the BDR gate.
- `RESEARCH`: a read-only fact needed by another item.
- `DELIVERY`: one vertical behavior slice with acceptance criteria and verification.
- `MANUAL`: an external or human action that blocks a decision or delivery slice.

A precise unanswered question is an item. A vague future concern stays under `Not yet specified` until evidence makes it precise.

## Build the frontier

1. Read the current parent item, linked work, active PRs, decisions, and code anchors.
2. List unresolved decisions before delivery work.
3. Draft only items that can be stated now.
4. Add blocking edges after every draft item has an identity.
5. Mark items with no open blockers as the frontier.
6. Keep titles readable. Use the title with its link in human output, not bare Jira keys.
7. Check for existing subtasks before proposing a new one.

Do not pre-slice unknown work. Resolving one decision may create, change, or remove later items.

## Delivery tickets

Each delivery ticket must fit one fresh agent session and produce one verifiable behavior slice. Include:

- parent and governing decision links;
- what behavior becomes possible;
- acceptance criteria;
- blockers;
- domain and workflow skills;
- verification boundary;
- explicit exclusions.

Avoid layer-only tickets such as "add model" or "add service" unless the layer itself is the independently verifiable result.

A wide mechanical migration may need several ownership batches instead of vertical slices. Every intermediate state must remain valid. Do not add temporary compatibility code only to make the ticket graph look incremental.

## Jira write boundary

For `PUBLISH`:

1. Show the map changes, item titles, parent, descriptions, blockers, labels, and links.
2. Re-read the parent and existing subtasks immediately before writing.
3. Execute only the approved creation or update set.
4. Re-read every created or changed item and return its key and URL.
5. Report partial success item by item. Do not retry blindly.

Do not transition, assign, close, or comment unless that exact action was part of the approved preview.

## Advance one item

Load the map, then choose one open, unblocked item. Resolve only that item in the session. Use `dfm-delegation` only for independent read-only research children.

Record the result in its own BDR, research artifact, ticket, or receipt. Update the map with one linked line. Stop and hand off rather than starting the next item.

## Output contract

Store the draft or updated map and full item table in one durable artifact. For `PUBLISH`, keep the exact approved preview and returned Jira IDs there too.

Return one DFM receipt as the final response. Include the mode, destination, current frontier, Jira writes or pending authorization, conflicts, missing authority, the single next item, and artifact URI. Do not reproduce the map in chat.

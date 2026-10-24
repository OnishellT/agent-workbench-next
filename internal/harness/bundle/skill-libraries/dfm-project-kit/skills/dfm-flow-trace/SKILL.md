---
name: dfm-flow-trace
description: "Use to explain how one current DFM code path works end to end at a pinned revision. Trace entry points, state, messages, integrations, retries, effects, and proving tests without inferring historical intent or diagnosing a specific incident."
---

# DFM flow trace

Explain the current mechanics of one bounded DFM flow. Follow real code and tests from trigger to observable outcome, cite every behavior-changing transition, and keep historical intent separate.

Read [DFM core](../dfm-core/SKILL.md) and the matching domain skill first. Open [repository sources](../dfm-core/references/repository-sources.md) only when another checkout or historical documentation is needed to interpret a current anchor; [evidence](../dfm-core/references/evidence.md) before final claims; [trust and data](../dfm-core/references/trust-and-data.md) before handling external or private material; and [receipt](../dfm-core/references/receipt.md) only before final output.

## Routing boundary

Use this workflow for questions such as `how does this flow work?`, `where does this state come from?`, or `what happens after this message arrives?`

Use another workflow when the requested outcome is different:

- historical or business rationale -> `dfm-context-research`;
- a concrete failed order, refund, notification, environment, or other symptom -> `dfm-incident-investigation`;
- impact of a proposed or implemented change -> `dfm-blast-radius`;
- correctness of a PR or branch -> `dfm-pr-review`;
- a technical design for changing the flow -> `dfm-implementation-plan`.

Do not convert an explanation into an incident report, review, design, or history essay.

## Pin the trace

Record repository, checkout, branch, HEAD, relevant ticket or PR when one governs scope, selected domain skill, and the exact flow question. Define:

- trigger and input shape;
- success outcome;
- failure or retry boundary the user wants included;
- environment when runtime evidence is relevant;
- explicit exclusions.

A trace is revision-specific. If HEAD or the target PR changes, invalidate affected steps and retrace the changed range.

## Find the entry point

Start from the real trigger rather than a familiar service name. Depending on the flow, this may be:

- HTTP or admin route;
- management command;
- SQS/SNS or worker handler;
- SAM parser or dispatcher;
- Shopify webhook, fulfillment request, or callback;
- COESP or Smart License operation;
- scheduled, retry, cleanup, or outbox process.

Verify registration and dispatch. Do not assume an imported function is reachable.

## Trace end to end

Follow definitions and references with language-aware tools. At each step, record code anchor, input, output, persisted state, and external effect.

Trace only the boundaries the flow reaches:

1. validation, parsing, normalization, and classification;
2. dispatch, service, builder, or processor selection;
3. transaction boundaries, models, constraints, identifiers, and state transitions;
4. message envelopes, queues, topics, correlation keys, outbox records, and consumers;
5. Shopify, SAM, COESP, HTTP, admin, notification, or other external operations;
6. partial success, exceptions, retry classification, idempotency, DLQ, and cleanup;
7. response, status, audit, metric, UI/admin, or other observable result.

Distinguish a function call from durable state and a requested external mutation from a confirmed one. Follow shared helpers only far enough to explain behavior that changes the answer.

## Prove the trace

Use current tests as executable evidence. Prefer tests that enter through the same public seam and assert state or output rather than private call order.

When authorized read-only runtime evidence is available, use a bounded query, debugger/REPL inspection, API read, or browser observation to confirm a disputed step. Do not add instrumentation, create tracked files, replay messages, resend, reprocess, restart, or mutate data from this workflow.

For every unproven transition, name:

- the missing evidence;
- why source reading is insufficient;
- the smallest separate check or access needed.

A search with no consumer or test is a result, not proof that no consumer exists outside the available sources.

## Separate mechanics from intent

Current code and runtime prove what happens now. They do not prove why the business chose it, whether Jira still wants it, or whether the shape is correct.

Keep these questions separate:

- Current mechanics come from pinned code, tests, or runtime evidence.
- Declared behavior comes from Jira or accepted decisions.
- Historical rationale belongs in `dfm-context-research`.
- Drift means reporting both sides without silently choosing one.

## Output contract

Fit a short trace into the receipt. If the steps, state lifecycle, and proof set need more than 500 tokens, write one `local://` artifact capped at roughly 900 tokens unless the user asks for a deeper trace. Include the numbered flow, code anchors, state and external effects, retry behavior, tests, and unproven boundaries.

Return one DFM receipt as the final response. Include the trigger, outcome, behavior-changing steps, strongest proof, material gap or drift, the artifact URI when created, and a next capability only when the evidence requires one. Do not repeat the trace after the receipt.

Remain M0. Do not edit code or tests, create instrumentation, mutate GitHub or Jira, perform authenticated browser writes, replay or reprocess data, restart services, deploy, or change production.
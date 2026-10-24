---
name: dfm-blast-radius
description: Use when a DFM change looks local but may affect callers, stored state, messages, retries, integrations, admin actions, or another order flow. Trace the hidden reach, prove the load-bearing safety facts, and return a read-only risk report.
---

# DFM blast radius

A caller list is not enough. Find the breakage that sits outside the diff, then prove the facts that make the change safe.

Read `dfm-core` and the matching domain skill first. Pin the ticket, PR or branch, base SHA, head SHA, changed files, and intended behavior.

## Scope the changed contract

Describe what changed in observable terms:

- accepted inputs;
- outputs and side effects;
- stored fields and constraints;
- status transitions;
- message or API shape;
- retry, duplicate, and idempotency behavior;
- environment or tenant scope.

Do not infer intent from code alone. Use Jira, accepted decision records, the active PR, current code, and tests.

## Trace beyond the diff

1. Find definitions, references, callers, subclasses, registrations, and tests with language-aware tools.
2. Follow data after the direct callers. Check database constraints, migrations, serialized payloads, queue consumers, SNS publishers, Shopify mutations, COESP handling, APIs, admin actions, metrics, and cleanup jobs when relevant.
3. Trace identifiers across boundaries. Distinguish request, order, line, Shopify order, fulfillment order, fulfillment item, source event, and processing keys.
4. Check every DFM domain touched by the changed contract. Load only those domain skills.
5. Search Git and PR history when compatibility or an old exception may explain the current shape.

A search with no callers or consumers is evidence. Record the query and scope.

## Find the safety facts

State the one to three facts the change depends on. Each fact must be specific enough to fail.

Examples of useful fact shapes:

- This status is read only by these two branches.
- This payload field is ignored by the only current consumer.
- This unique constraint prevents a second active fulfillment claim.
- This replay republishes stored bytes and does not rebuild mutable state.

Do not write broad claims such as "tests cover it" or "the helper is isolated."

## Prove each fact

Use the strongest proof that is cheap and relevant:

1. `CITED`: current source or contract names the fact.
2. `TRACED`: the full reachable path excludes the bad case.
3. `EXECUTED`: an existing focused test, query, or temporary script exercises the real path.
4. `OBSERVED`: the authorized runtime path shows the expected behavior.

Use existing tests first. A temporary proof belongs outside tracked files. This skill remains read-only and must not add a regression test or production change.

Mark a fact `UNPROVEN` when it cannot reach the needed proof level. Do not round it up.

## DFM risk checks

Apply only the checks reached by the change:

- legacy and NextGen message compatibility;
- digital, physical, rental, pickup, ship-to, refund, cancel, and mixed-line behavior;
- tenant isolation;
- duplicate, retry, DLQ, outbox, and replay semantics;
- money, tax, shipping, and fee calculations;
- Shopify and COESP identifiers and error classification;
- Smart License redaction and delayed activation;
- API response keys, casing, authentication, and status semantics;
- admin, observability, retention, and recovery paths.

For each risk, report likelihood, impact, evidence, and the cheapest decisive check. Remove speculative risks that the evidence clears.

## Delegation

For a wide change with independent domains, read `dfm-delegation`. Split read-only tracing by domain in one batch. The parent owns the changed-contract map, proof, risk ranking, and final report.

Do not use several agents to repeat the same review. Do not delegate runtime, browser, production, or external mutations.

## Output contract

If the proof and risk tables do not fit the receipt, write one `local://` report with the snapshot, changed contract, safety facts and proof levels, confirmed risks, cleared paths, unproven facts, and smallest required check.

Return one DFM receipt as the final response. Include the safety result, confirmed risks, every unproven load-bearing fact, the required check, and the report URI when created. Do not repeat the report in chat.

Re-read the PR head before finalizing. A changed head invalidates the report.

Remain M0. Do not edit code, create tests, publish comments, update Jira, push, rebase, merge, replay, resend, deploy, or mutate an environment.

---
name: dfm-qa-validation
description: "Use to plan or execute DFM QA against a ticket, PR, environment, browser flow, API, or payload, and to generate bounded synthetic test data. Select PLAN, EXECUTE, or TEST_DATA mode. Do not modify implementation code or silently submit generated data."
---

# DFM QA Validation

Turn requirements and active business rules into observable scenarios. Test only the declared environment and side-effect boundary.

Read [DFM core](../dfm-core/SKILL.md) and the matching domain skill first. Open [repository sources](../dfm-core/references/repository-sources.md) only when historical documentation or another checkout matters; [decision context](../dfm-core/references/context.md) when rules are time-bound; [trust and data](../dfm-core/references/trust-and-data.md) before handling payloads or external data; [mutations](../dfm-core/references/mutations.md) before `EXECUTE`; [evidence](../dfm-core/references/evidence.md) before a readiness claim; and [receipt](../dfm-core/references/receipt.md) only before final output.

## Select one mode

- `PLAN`: produce scenarios and required evidence without executing.
- `EXECUTE`: run authorized scenarios and record results.
- `TEST_DATA`: generate bounded synthetic payloads or setup data without submitting them.

Infer the mode from the requested outcome. Ask one focused question only if execution versus planning or the target environment is materially ambiguous.

## Establish the test contract

Pin ticket requirements, applicable decisions/business rules, PR head SHA when present, environment, tenant/account/store, allowed data, and authorized side effects.

1. Map each requirement or invariant to one or more scenarios.
2. Select only relevant dimensions. Consider order type, fulfillment mode, item cardinality, price/refund shape, retry/idempotency, and missing/malformed dependencies only when the ticket or code makes them relevant.
3. Define preconditions, action, expected state transitions, observability, cleanup, and pass/fail criteria.
4. Select the narrow domain source. Use `docs/payloads/REGISTRY.md`, the relevant contract extract, and current message parsers, validators, and tests. Verify every schema against active code.
5. In TEST_DATA mode, determine whether the input is raw message content, an SNS envelope, a NextGen JSON message, or a Shopify webhook envelope before generating it. Use synthetic or redacted identifiers and mark environment compatibility.
6. Keep one-off scenario variants outside tracked docs. Commit fixtures only when they encode a new reusable contract and update the current registry.
7. Generation does not authorize submission.

## Execute

1. Immediately before a write, verify canonical URL, environment, tenant/account, and exact target.
2. Execute each authorized scenario once unless retry behavior is under test.
3. Capture request/action, response, downstream state, UI/admin evidence, and timing needed to judge the contract.
4. Map every result back to requirement IDs.
5. Re-read volatile state before declaring readiness.
6. If behavior fails, do not repair code. Produce an incident-ready receipt.

## Output contract

Store the complete scenario matrix and synthetic payloads or evidence set in one bounded artifact.

Return one DFM receipt as the final response. Include the mode, environment, readiness result, failed or blocked scenarios, cleanup state, unexplained failures, artifact URI, and `dfm-incident-investigation` only when needed. Do not copy the matrix or payloads into chat.

Do not change implementation code, publish results, transition Jira, resend/reprocess, or perform production writes without their dedicated authorization and workflow.

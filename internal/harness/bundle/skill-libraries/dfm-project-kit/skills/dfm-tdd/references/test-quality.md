# DFM test quality

A durable test protects behavior through a stable boundary.

## Good tests

- Name the capability or failure mode.
- Exercise the public seam used by callers or workers.
- Use independent expected values from Jira, a contract, a worked example, or known domain rules.
- Fail when the behavior regresses.
- Survive internal refactoring.
- Keep one logical behavior per test.

## Reject these tests

- Internal call-count assertions when the contract is the output or state.
- Private-method tests used to avoid the real seam.
- Expected values computed with the same algorithm as production.
- Conditional assertions that can skip the assertion.
- Mocks whose shapes differ from Shopify, COESP, SAM, or queue input.
- A green test that never went red on the target behavior.
- A broad snapshot when a direct contract assertion is available.

## DFM examples of seams

- Legacy or NextGen parser input to typed result or validation error.
- Processor input to local state and outbound intent.
- API request to exact response body and status semantics.
- Transactional repository operation to persisted state and idempotency result.
- Outbox event creation, claim, payload, and publish state.
- Fulfillment service input to unit-level audit and external request intent.

The seam is wrong when the real bug can occur while the test still passes.

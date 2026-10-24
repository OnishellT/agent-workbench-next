---
name: dfm-sam-nextgen
description: Use for SAM NextGen JSON validation, digital, physical, rental, auto-sale, refunds, pickup, fulfillment, tax, outbound responses, event outbox, replay, or NextGen admin actions.
disable-model-invocation: true
---

# DFM SAM NextGen

## Read first

- `docs/architecture.md#nextgen-scope-in-current-main`
- `docs/data-and-state.md#durable-publication`
- `docs/workers-and-messages.md#sam-nextgen`
- `sam_nextgen_integration/OPERATIONS.md` for rollout, replay, or recovery
- Current `sam_nextgen_integration/` code, Git history, and focused tests

## Current shape

NextGen is not digital-only. `constants.py` defines digital, physical, rental, auto-sale, and refund families. `dispatcher.py` chooses digital or physical processing. Physical work has separate builder and processor modules. Shared SAM models store request, order, line, response, outbox, and refund lifecycle state.

## Contract checks

- Keep primitive validation strict. Do not coerce invalid booleans or numbers.
- Preserve request and line identities through duplicate and retry paths.
- Preserve tenant filtering at every lookup.
- Keep digital and physical concepts separate in types and processors.
- Verify shipping, tax, pickup, substitution, refund, and fulfillment behavior only when the task touches them.
- `SAMOutboxEvent` stores exact payload bytes before publication. Retries reuse them.
- Outbox delivery is at least once. Keep stable idempotency attributes and consumer deduplication requirements.
- Use line-level refund lifecycle records for partial and mixed physical outcomes.
- Add focused tests for the changed dispatch, validation, builder, processor, repository, outbound, and replay contracts.

## Avoid

Do not rely on the older digital-only contract extract for current physical or outbox behavior. Do not replay source queues before checking existing outbox events and the operations runbook.

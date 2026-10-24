---
name: dfm-legacy-sam
description: Use for legacy SAM XML or ECEDOC intake, classification, idempotency, Shopify creation, physical or digital flows, refunds, rentals, payload generation, responses, or pickup status.
disable-model-invocation: true
---

# DFM legacy SAM

## Read first

- `docs/architecture.md`
- `docs/data-and-state.md`
- `docs/workers-and-messages.md`
- `.github/instructions/sam-review.instructions.md`
- `docs/contracts/sam-legacy-ecedoc.md` and `docs/payloads/REGISTRY.md` only when exact shape or test data matters
- Current `sam_integration/` code and focused tests

## Contract checks

- Direct SQS tests use an SNS envelope whose `Message` contains XML or ECEDOC text.
- Preserve ECEDOC fixed widths.
- Preserve `OrdRefNo` order correlation and line-level `SrcTransID` idempotency.
- Reuse `sam_integration/validators.py` and shared retry/DLQ helpers.
- Keep money calculations explicit. Do not use raw float arithmetic.
- Preserve transaction context in logs and metrics.
- For processor, classifier, or idempotency changes, cover new order, duplicate without retry, retry with an existing Shopify order, and retry without one.
- Treat request status as a summary. Use order, line, response, and refund lifecycle records for multi-line diagnosis.

## Avoid

Do not use old fixture comments or handbook text as current behavior without checking main. Do not import NextGen JSON terms into legacy XML or ECEDOC contracts.

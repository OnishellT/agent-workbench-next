# DFM debugging

Start with one stable identity. Trace the earliest incorrect state. Do not begin by replaying messages or changing admin state.

## Choose a trace key

Use the strongest key available:

- legacy order: `OrdRefNo` and line `SrcTransID`;
- NextGen order: SAM order reference plus line IDs and source event IDs;
- Shopify: order ID or name, fulfillment order ID, fulfillment item ID, and line-item ID;
- COESP: `espCentralTransactionId` or the stored fulfillment ID;
- queue delivery: webhook ID, source message ID, request ID, or processing key;
- outbox: response type, source event ID, correlated line ID, and idempotency key.

Do not search by a display name when an immutable ID exists.

## Trace order

1. Pin environment, tenant, code revision, and incident time.
2. Find `SAMOrderRequest` by request identity and inspect source, type, status, raw payload, errors, tenant, and Shopify IDs.
3. Find related `SAMOrder`, `SAMOrderLine`, `SAMOrderResponse`, outbox, and refund lifecycle rows.
4. Inspect `PayloadLog`, `OrderProcessingFailure`, and lifecycle events for Shopify-originated work.
5. Inspect `FulfillmentAudit` per fulfillment unit. One order can have several audits.
6. Check Shopify timeline and fulfillment state when local records show a Shopify ID.
7. Check COESP or Smart License state only after the local boundary indicates provisioning started.
8. Compare the observed transition with current code and focused tests.

## Fast status split

- `FAILED` usually points to intake, validation, product lookup, classification, or Shopify order creation.
- `PROVISION_FAILED` points to provisioning after an order reached the fulfillment path.
- `PENDING_RETRY` and `pending_retry` mean retry work remains. Check receive count, next retry, and transient cause before manual action.
- `SMART_LICENSE_PENDING` is waiting for activation, not a terminal failure.
- Partial refund or cancellation requires line-level lifecycle and Shopify line mapping. Request status alone is not enough.

Read [data and state](data-and-state.md) for the current enum values.

## Current diagnostics

`OrderProcessingFailure` aggregates failures by processing key and order. It records stage, status, count, time range, error code, and optional payload link. It is controlled by the observability rollout flag.

Structured lifecycle events use the `DigitalFulfillment/OrderProcessing` namespace. Processing keys prefer webhook identity, then source message identity, then order and topic. The lifecycle and persistence paths sanitize identifiers and error codes.

Grafana templates, alert rules, and their tests live under `ops/order-processing-observability/`.

## NextGen publication

For missing or duplicate NextGen responses, inspect `SAMOutboxEvent` before replaying a queue message. The outbox stores the exact payload and claim state. Read `sam_nextgen_integration/OPERATIONS.md`. An SNS success followed by a database update failure can produce a duplicate delivery with the same idempotency key.

## Admin actions

Admin includes resend, replay, reprocess, response regeneration, and pickup cache invalidation actions. These actions mutate external or local state. Read their current implementation before use. Verify the selected environment and records. Preview the exact target. Execute once. Confirm the resulting audit and downstream state.

## Historical handbook

The handbook debugging cookbook has useful symptom routes. Its branch predates substantial NextGen, outbox, refund lifecycle, and observability work. Use it to form hypotheses, then verify every field, status, and path against main.

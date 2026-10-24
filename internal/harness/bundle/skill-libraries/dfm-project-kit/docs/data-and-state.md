# DFM data and state

Use this map to find the owner of an identifier or status. Read the cited model before making a behavioral claim.

## Core records

| Record | Location | Purpose |
| --- | --- | --- |
| `Tenant` | `shopify_configs/models.py` | Shopify shop identity and encrypted configuration. |
| `SAMOrderRequest` | `sam_integration/models.py` | Request-level intake, source, raw payload, tenant, Shopify link, provisioning state, and errors. |
| `SAMOrder` | `sam_integration/models.py` | Order or line-level correlation with Shopify and lifecycle state. |
| `SAMOrderLine` | `sam_integration/models.py` | Per-line identifiers, Shopify line mapping, pricing, tax, fulfillment, refund, and substitution data. |
| `SAMOrderResponse` | `sam_integration/models.py` | Audit of responses sent back to SAM. |
| `SAMOutboxEvent` | `sam_integration/models.py` | Immutable NextGen response payload plus claim and publish state. |
| `SAMRefundLineLifecycleEvent` | `sam_integration/models.py` | Idempotent reservation and application record for one refund event line. |
| `PickupLocationCache` | `sam_integration/models.py` | Per-tenant and store pickup handle cache with a seven-day TTL. |
| `FulfillmentAudit` | `shopify_orders/models.py` | Unit-level Shopify and COESP fulfillment lifecycle. |
| `PayloadLog` | `shopify_orders/models.py` | Incoming Shopify payload audit. |
| `OrderProcessingFailure` | `shopify_orders/models.py` | Aggregated order-processing incident keyed by delivery and order. |

## Identity rules

- `SAMOrderRequest.sam_transaction_id` is the request-level SAM identity.
- `SAMOrder.src_trans_id` is unique. Legacy digital uses the line `SrcTransID`; NextGen physical uses the first line transaction identity.
- `OrdRefNo` is the legacy order correlation key.
- Shopify order, fulfillment order, fulfillment order item, and line-item IDs are different identifiers. Do not substitute one for another.
- `SAMOutboxEvent` uses source event, source line, correlated SAM line, and stable idempotency key data.
- Order-processing observability derives `processing_key` in this order: webhook ID, source message ID, then order ID plus topic. Every key includes tenant identity.

## Request status

`SAMOrderRequest.Status` currently includes:

```text
PENDING
SUCCESS
PARTIAL_SUCCESS
FAILED
PROVISIONING
PROVISIONED
PROVISION_FAILED
PENDING_RETRY
SMART_LICENSE_PENDING
RETURNED
PARTIAL_REFUNDED
REFUNDED
CANCELLED
PARTIAL_CANCELLED
```

`SAMOrderRequest.source` distinguishes `sam_legacy` and `sam_nextgen`.

A request status summarizes a flow. For split physical orders, line and order records can hold more precise state.

## Order status

`SAMOrder.ProcessingStatus` currently includes:

```text
pending
shopify_created
fulfilled
failed
refunded
partial_refunded
cancelled
```

`SAMOrder.RefundLifecycleStatus` tracks partial or complete refunded, cancelled, and mixed outcomes. Use it with line-level lifecycle events when diagnosing a multi-line refund.

## Fulfillment audit status

`shopify_core.models.FulfillmentStatus` currently includes:

```text
processing
completed
failed_fatal
pending_retry
smart_license_pending
canceled
cancellation_failed
cancellation_pending_retry
```

A partial unique constraint allows only one active audit per fulfillment unit. Terminal rows remain as history so a later manual retry can create a new audit.

## Durable publication

`SAMOutboxEvent` stores the exact serialized NextGen payload before SNS publication. Its states are `PENDING`, `PUBLISHING`, `PUBLISHED`, and `FAILED`. A retry republishes the stored bytes. Delivery remains at least once, so consumers must deduplicate with the stable idempotency key.

Read `sam_nextgen_integration/OPERATIONS.md` before rollout, rollback, replay, or recovery work.

## Processing incidents

`OrderProcessingFailure` records stage, status, identifiers, first and last failure times, count, error code, and an optional payload log. Stages cover ingest, publish, OAuth, Shopify API, fulfillment, and queue work. Statuses are retrying, recovered, failed, and dead-lettered.

The persistence path is best effort. It must not become a fulfillment precondition. Lifecycle events and Grafana assets live under `utils/order_processing_lifecycle.py` and `ops/order-processing-observability/`.

# DFM glossary

Use these definitions to choose code and documentation. Verify exact field semantics in the cited models and contracts.

## Maintenance

The glossary stores domain language, not implementation detail. Keep one canonical term and list aliases only when people or source systems use them.

Before changing a term:

1. Name the conflicting or overloaded uses.
2. Test the proposed definition with concrete DFM scenarios.
3. Compare the definition with current code, Jira, decisions, and rules.
4. Record disagreement instead of changing the glossary to match code silently.

`dfm-decision-capture` owns active glossary changes that arise from a reviewed decision. Reading this file for vocabulary does not invoke domain modeling.

## Systems

### DFM

Digital Fulfillment Manager. The Django service documented here.

### SAM legacy

The XML and fixed-width ECEDOC integration handled by `sam_integration`.

### SAM NextGen

The strict JSON integration handled by `sam_nextgen_integration`. Current main supports digital, physical, rental, auto-sale, and refund families.

### Shopify

The commerce system that owns orders, line items, fulfillment orders, refunds, and webhooks used by DFM.

### COESP

The digital provisioning and cancellation service. HTTP success does not prove business success. DFM evaluates `responseCode`.

### Smart License

A two-step provisioning path. Initial COESP processing can leave the request pending until an activation event completes or fails it.

## Local records

### Tenant

The local Shopify shop identity and configuration boundary.

### SAM order request

`SAMOrderRequest` stores request-level intake, source, payload, tenant, Shopify links, status, and errors.

### SAM order

`SAMOrder` stores an order or transaction line relationship with Shopify and its processing/refund state.

### SAM order line

`SAMOrderLine` stores line identity, Shopify mapping, price, tax, fulfillment, refund, and substitution detail.

### Fulfillment audit

`FulfillmentAudit` records one unit of Shopify and COESP fulfillment work. An order can have several audit rows.

### SAM outbox event

`SAMOutboxEvent` stores one immutable NextGen response payload before publication. It tracks claim, attempts, and publish state.

### Order-processing failure

`OrderProcessingFailure` is a compact incident record for a failed processing chain and order.

## Identifiers

### `OrdRefNo`

Legacy SAM order correlation key.

### `SrcTransID`

Legacy line-level source transaction and idempotency key. It also participates in current SAM order persistence.

### Processing key

A tenant-scoped lifecycle correlation key. Current code prefers Shopify webhook ID, then source message ID, then order ID plus topic.

### Shopify fulfillment order

Shopify work assigned to a location or service. It is not the Shopify order, line item, or fulfillment order item.

### Source event ID

The immutable external event identity used by NextGen outbox and refund lifecycle records, such as a Shopify refund ID.

## Message terms

### SNS envelope

The wrapper expected when legacy SAM content is sent directly through SQS. Its `Message` field contains XML or ECEDOC text.

### DLQ

Dead-letter queue. Generic worker DLQs and legacy SAM DLQs do not use the same redrive shape.

### At-least-once delivery

A message can arrive more than once. NextGen outbox consumers must apply the stable idempotency key with the business side effect.

# DFM workers and messages

Use the active `Makefile` and command source before running a worker. This page names the current entry points and message families. It does not copy environment URLs or credentials.

## Worker map

| Command | Make target | Input or role |
| --- | --- | --- |
| `process_paid_orders` | `run_om_worker`, `watch_orders` | Shopify paid-order queue. |
| `process_fulfillment_requests` | `process_fulfillment_requests`, `watch_fulfillments` | Shopify fulfillment requests and COESP provisioning. |
| `process_fulfillment_responses` | `process_fulfillment_responses`, `watch_fulfillment_responses` | COESP responses and Shopify physical webhook events. |
| `process_cancellation_requests` | `watch_cancelled` | Shopify cancellation requests. |
| `process_sam_orders` | `process_sam_orders`, `watch_sam_orders` | Legacy SAM XML or ECEDOC intake. |
| `process_one_sam_order` | `process_one_sam_order` | Confirmed one-message legacy rollout tool. |
| `process_sam_nextgen_orders` | `process_sam_nextgen_orders`, `watch_sam_nextgen_orders` | SAM NextGen JSON intake. |
| `process_one_sam_nextgen_order` | `process_one_sam_nextgen_order` | Confirmed one-message NextGen rollout tool. |
| `process_smart_license_activations` | `process_smart_license_activations`, `watch_smart_license_activations` | Smart License activation events. |
| `cleanup_order_processing_failures` | no fixed Make wrapper | Bounded retention cleanup with dry-run and scoped deletion controls. |

Workers derive queue and DLQ names from environment variables. Verify the active environment before starting one. A local command can still target shared AWS resources.

## Message families

### Legacy SAM

Direct SQS sends use an SNS notification envelope. The envelope `Message` contains legacy XML or fixed-width ECEDOC text. Preserve ECEDOC widths. Change order and line transaction IDs unless duplicate behavior is the test.

Read `docs/contracts/sam-legacy-ecedoc.md`, `docs/payloads/REGISTRY.md`, and the current parser/tests for exact fields and safe generation policy.

### SAM NextGen

NextGen consumes raw JSON from its dedicated queue. Current dispatch covers digital, physical, rental, auto-sale, and refund types. Validation is strict. Do not coerce invalid primitive types.

Responses publish to the configured NextGen response topic. Event-scoped responses use `SAMOutboxEvent` and stable message attributes. Read `sam_nextgen_integration/OPERATIONS.md` for delivery and replay rules.

### Shopify paid orders

The worker expects parsed message content with `headers` and `body`. Headers include shop domain, topic, webhook ID, request ID, processing key, and queue time when available. The worker emits correlated lifecycle events and can persist compact failure diagnostics.

### Fulfillment requests

The worker fetches assigned fulfillment orders from Shopify. It groups units, preserves digital fee pairing, claims active audit rows, and calls COESP when required. Retry claims and stale claims are per fulfillment unit.

### Fulfillment responses

The response worker handles COESP fulfillment-response content and Shopify physical webhook content. Do not infer shape from queue name alone. Inspect headers, topic, and body before dispatch.

### Smart License

The activation worker correlates `espCentralTransactionId` and `sourceSystemTransactionId`, updates local and Shopify state, publishes final responses, and separates malformed, transient, fatal, and non-SAM cases. Sensitive access URLs and passcodes must remain redacted.

## Retry and DLQ

Most workers use the shared worker pool and retry helpers. Keep transient and fatal errors distinct. Do not add local retry loops around shared retry/DLQ handling.

Legacy SAM DLQ redrive preserves the original SNS envelope. Generic worker DLQs can contain an operational wrapper. A wrapper is not automatically a valid source-queue message.

## Payload generation

Use `make payload_generate` or `make payload_generate_all` after verifying the active targets. Generate one-off scenarios outside tracked docs. Add a fixture only when it represents a reusable contract that tests cannot construct more clearly, then document it in `docs/payloads/REGISTRY.md`.

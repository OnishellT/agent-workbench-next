# DFM payload registry

This directory records payload policy and message families. It does not store environment-ready or customer-derived payloads.

Start with `docs/workers-and-messages.md`. Generate a synthetic payload only after choosing the worker, message family, environment, and expected state transition.

## Message families

| Family | Source body |
| --- | --- |
| Legacy SAM digital | XML inside an SNS notification envelope when sent directly to SQS. |
| Legacy SAM physical | Fixed-width ECEDOC text inside an SNS notification envelope. |
| SAM NextGen | Raw JSON from the envelope `Message` value when using the current generator. |
| Shopify workers | Parsed message content with `headers` and `body`. |
| Generic worker DLQ | Operational wrapper. Do not send it to a source queue without translating it to that queue's contract. |

## Current generator types

The source of truth is `digital_fulfillment/sam_integration/payload_generation.py`.

Legacy types:

```text
digital_normal_order
digital_duplicate
digital_refund
physical_normal_order
physical_duplicate
physical_auto_refund
physical_refund
physical_rental
```

NextGen types:

```text
next_gen_digital_normal_order
next_gen_digital_instructor_order
next_gen_digital_no_materials_order
next_gen_digital_refund
next_gen_physical_normal_order
next_gen_physical_refund
```

The standalone CLI does not expose every subtype, fulfillment method, and line-correlation argument required by current physical NextGen generation. Use a verified code or admin path until the CLI supports the needed fields.

## Generation policy

- Use synthetic or redacted values.
- Change order, line, batch, webhook, and source-event IDs unless duplicate behavior is under test.
- Preserve ECEDOC fixed widths.
- Generate one-off files in a scratch directory.
- Do not commit environment URLs, queue ARNs, credentials, access links, or customer data.
- Add a fixture only when it represents a reusable contract shape that tests cannot construct more clearly.

Generation does not authorize submission.

## Verification

Before sending a payload:

1. Confirm the worker and source queue contract.
2. Parse and validate the payload with current code.
3. Confirm environment, account, tenant, queue or topic, and allowed side effects.
4. Record expected local, Shopify, COESP, SAM response, outbox, and audit state as applicable.
5. Use unique identities unless testing replay or idempotency.
6. Define cleanup and evidence before execution.

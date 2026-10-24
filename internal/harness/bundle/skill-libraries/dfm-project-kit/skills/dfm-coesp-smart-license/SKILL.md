---
name: dfm-coesp-smart-license
description: Use for COESP provisioning or cancellation, Smart License activation, responseCode semantics, fulfillment audits, retry classification, DLQ behavior, correlation, redaction, or status propagation.
disable-model-invocation: true
---

# DFM COESP and Smart License

## Read first

- `docs/architecture.md`
- `docs/data-and-state.md#fulfillment-audit-status`
- `docs/workers-and-messages.md#smart-license`
- `.github/instructions/coesp-review.instructions.md`
- `.github/instructions/smart-license-review.instructions.md`
- Current `coesp_client/`, `smart_license/`, and relevant fulfillment code/tests

## Contract checks

- HTTP 200 is transport success only. Evaluate COESP `responseCode` for business success.
- Use central error constants and classification helpers.
- Preserve transient and fatal error meaning through retries, DLQ, local status, and Shopify actions.
- Keep request, source-system, COESP, Shopify, and fulfillment identifiers in safe logs and metrics.
- Preserve parser error types for malformed Smart License payloads.
- Keep access URLs, passcodes, credentials, and raw activation payloads redacted.
- A non-SAM activation can be an acknowledged skip. Verify current parser and service behavior before treating it as a DLQ case.
- Test malformed input, missing identifiers, timestamps, transient/fatal mapping, retry/DLQ, cancellation, activation, and downstream status effects only as applicable.

## Avoid

Do not flatten every COESP error into one retry path. Do not treat transport success as provisioning success. Do not log raw sensitive payloads.

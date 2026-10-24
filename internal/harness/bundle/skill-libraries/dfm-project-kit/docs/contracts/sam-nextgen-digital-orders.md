# SAM NextGen digital orders contract extract

This is a digital-orders version 1.0 contract extract dated 2026-06-02. It is not the complete current NextGen contract. Current main also implements physical, rental, auto-sale, refund, fulfillment, pickup, tax, and event-outbox behavior. Start with `docs/architecture.md` and verify exact fields in current types, validators, builders, processors, outbound code, and tests.

## Scope

- SAM NextGen publishes JSON order messages to a dedicated DFM queue.
- DFM processes this path in parallel with the legacy SAM XML/ECEDOC queue.
- DFM publishes responses through SNS.
- One outbound response message is emitted per line item.
- Pilot scope was digital orders for Delaware CC store 1177 / Course Charge / Summer II 2026.

## Core design decisions

- `order_type` is the explicit classifier. Do not infer NextGen order type from legacy fields such as `OrdType`, `OrigOrdRefNo`, `ItmCustFld2`, item description keywords, quantity sign, or price sign.
- Payloads are flat JSON objects at the root. There is no `order` wrapper.
- `schema_version` exists for breaking changes.
- Unknown fields must be ignored. Additive fields do not require a schema version bump.
- Removed legacy identifiers include `sam_order_id`, `store_id`, `batch_id`, `patron_id`, and `OHID`.
- `billing` is required on new order types and omitted for `digital_refund`.

## Order types

| `order_type` | Meaning |
| --- | --- |
| `digital_student` | New paying-student digital sale. Continuation orders use this with `original_order_ref` populated. |
| `digital_instructor` | Complimentary instructor digital copy. |
| `digital_no_materials` | Bypass / no-materials SKU. DFM creates a fulfilled Shopify order and bypasses COESP. |
| `digital_refund` | Refund or cancellation of a digital order. |

Removed from the initial proposal: `digital_trueup_positive`, `digital_trueup_negative`, and `digital_continuation` as standalone order types.

## Root fields

Required or common root fields:

- `schema_version`: string, currently `"1.0"`.
- `order_type`: one of the supported order types.
- `sent_at`: ISO 8601 UTC timestamp.
- `store_number`: zero-padded string.
- `sam_order_ref`: SAM order reference.
- `original_order_ref`: null on new orders; populated for refunds and continuations.
- `is_retry`: boolean. Do not accept string booleans.
- `is_duplicate`: boolean, default false. Mirrors legacy DUP classifier behavior.
- `currency_code`: usually `USD`.
- `total_price`: decimal string.
- `tax_exempt`: boolean.
- `placed_at`: ISO 8601 UTC timestamp.
- `financial_aid_code`, `charge_account_number`, `collateral_account_number`, `payment_method`: nullable strings.
- `customer`: required object.
- `billing`: required except on `digital_refund`.
- `line_items`: non-empty list.

## Customer and billing

`customer` contains:

- `first_name`
- `last_name`
- `email`
- `phone`
- `sis_user_id`
- `school_student_id`
- `student_id`

`billing` contains the same person/contact shape plus an `address` object:

- `line1`
- `line2`
- `city`
- `state`
- `postal_code`
- `country`

## Line item fields

Each `line_items[]` entry contains:

- `line_id`
- `line_number`
- `src_trans_id`
- `isbn`
- `sku`
- `upc`
- `vfi`
- `custom_id`
- `title`
- `is_smart_license`: boolean. Pilot value is false.
- `is_bypass`: boolean.
- `quantity`: integer.
- `unit_price`: decimal string.
- `retail_price`: nullable decimal string.
- `course`: object with `name`, `number`, `section`, `department_code`, `division_code`, and `lms_course_id`.
- `term`: object with `id`, `name`, `start_date`, and `last_drop_date`.
- `subscription`: null unless a supported subscription product path is introduced.

`src_trans_id` is the line-level idempotency key.

## Duplicate behavior

When root `is_duplicate` is true:

- DFM skips COESP provisioning.
- DFM creates the Shopify order as fulfilled.
- DFM sends only a `FULFILLMENT_NOTIFICATION`.

## Refund and cancel behavior

`digital_refund` requires refund-specific original identifiers. Current code and PR history define the exact branch behavior for:

- `FULFILLMENT_REFUNDED`
- `FULFILLMENT_CANCELLED`
- `CANCEL_UNKNOWN_ORDER`
- `CANCEL_ALREADY_CANCELED`
- `ESP_DENIED`

Use current Git history and `digital_fulfillment/sam_nextgen_integration/` before changing refund or cancel behavior.

## Tax fields

The v1.0 contract added tax fields to success and refund/cancel notifications:

- `tax_amount`: pre-summed decimal string.
- `tax_lines`: per-jurisdiction breakdown.

Both fields are omitted for tax-exempt stores. `tax_amount` must equal the sum of `tax_lines[*].amount`.

## Implementation anchors

- Types: `digital_fulfillment/sam_nextgen_integration/types.py`
- Validation: `digital_fulfillment/sam_nextgen_integration/validators.py`
- Processing: `digital_fulfillment/sam_nextgen_integration/processor.py`
- Outbound responses: `digital_fulfillment/sam_nextgen_integration/outbound.py`
- Recent behavior changes: current Git and PR history.

## Agent rules

- Do not coerce string booleans into booleans.
- Do not import legacy ECEDOC field names into NextGen JSON concepts.
- Prefer current code and tests over this source extract when they disagree.
- Retrieve the approved source document when exact contract intent is unresolved.

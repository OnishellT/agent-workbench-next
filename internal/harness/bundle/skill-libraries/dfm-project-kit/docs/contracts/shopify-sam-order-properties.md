# SAM-driven Shopify order properties extract

This file summarizes the agent-relevant parts of the SAM order properties and metafields contract. It replaces binary-first routing to the original PDF.

Current code and tests remain higher authority. Use this file for orientation, then inspect `digital_fulfillment/sam_integration/`, `digital_fulfillment/shopify_core/`, and `digital_fulfillment/shopify_fulfillments/`.

## Common order properties

All SAM-driven Shopify orders are expected to carry a common Access identity surface:

- `note`: `SAM Order: {ord_ref_no}`
- `processedAt`: placement timestamp from SAM when available.
- Base tag: `ACCESS`

## Common custom attributes

The original contract describes `fm_access_*` custom attributes intended for admin visibility. Missing values should be represented as empty strings when the attribute set is emitted.

| Attribute | Meaning |
| --- | --- |
| `fm_access_order_classifier` | DFM classifier, such as `digital_sale` or `physical_trueup_positive`. |
| `fm_access_order_number` | SAM order or transaction reference. |
| `fm_access_fulfillment_type` | `digital` or `physical`. |
| `fm_access_material_type` | SAM material type, such as `DIGITAL` or `TEXT`. |
| `fm_access_ddcs_division` | Product taxonomy division. |
| `fm_access_ddcs_department` | Product taxonomy department code. |
| `fm_access_ddcs_course` | Course number. |
| `fm_access_ddcs_section` | Section name. |
| `fm_access_term` | Academic term. |
| `fm_access_user_role` | Student or instructor. |
| `fm_access_user_id` | SAM student/user identifier. |

## SAM integration metafields

The source contract describes mutable or large SAM data in the `sam_integration` namespace:

| Metafield | Type | Meaning |
| --- | --- | --- |
| `_smartlicense_status` | `single_line_text_field` | Smart License provisioning status such as `pending`, `activated`, or `na`. |
| `_sam_order_payload_history` | `multi_line_text_field` | Complete SAM XML payload history. |
| `_pickup_status` | `single_line_text_field` | Physical pickup status such as `pending`, `ready`, or `picked_up`. |

`_pickup_status` is physical-only.

## Digital order families

Digital order classification depends on current code, but the contract families include:

- Student sale.
- Instructor copy.
- No-materials / bypass.
- Refund.
- Continuation.
- Positive and negative true-up where supported by the legacy flow.

Common digital line-item metadata includes product taxonomy values and `_is_digital = "true"` where the current implementation emits it.

## Physical order families

Physical order families include:

- Physical sale.
- Physical refund.
- Physical true-up positive/negative.
- Auto sale/refund.
- Duplicate.
- Rental pickup/ship-to paths where supported by current code.

Physical pickup behavior depends on `NONE` / `NONE` shipping carrier and method in ECEDOC fixtures.

## Implementation anchors

- Classifier: `digital_fulfillment/sam_integration/order_classifier.py`
- Order builders: `digital_fulfillment/sam_integration/order_builder.py`, `draft_order_builder.py`, `ship_to_order_builder.py`
- Product lookup: `digital_fulfillment/sam_integration/product_lookup.py`
- Shared Shopify constants and fee helpers: `digital_fulfillment/shopify_core/constants.py`, `digital_fee_utils.py`
- Smart License status handling: `digital_fulfillment/smart_license/`

## Agent rules

- Do not assume every property in the old source contract is emitted on every current code path.
- Preserve product/digital-fee grouping behavior.
- Preserve both nested and flat Shopify line-item shapes where current worker code accepts both.
- Verify current code and tests before changing tags, custom attributes, metafields, or pickup status behavior.


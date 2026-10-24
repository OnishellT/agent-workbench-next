# Legacy SAM / MMS ECEDOC contract extract

This is an agent-facing extract of the legacy SAM/MMS inbound and outbound message specifications. It replaces binary-first routing for prior PDF, DOCX, and XLSX source files.

Current code and tests remain higher authority. Use this file for field-shape orientation before inspecting `digital_fulfillment/sam_integration/`.

## Message families

DFM sees two legacy SAM message shapes:

- Digital XML: `<Orders><Order>...</Order></Orders>` or a single `<Order>`.
- Legacy physical/fixed-width XML: `<ECEDOC>...</ECEDOC>`.

Direct SQS sends for legacy SAM intake must use the full SNS notification envelope. The envelope `Message` value contains the XML/ECEDOC string.

## ECEDOC inbound order shape

The inbound order construction document describes this hierarchy:

```xml
<ECEDOC>
  <PROLOG>...</PROLOG>
  <HEADER>
    <HDR010>...</HDR010>
    <HDR020>...</HDR020>
    <HDR030>...</HDR030>
    <USRLST>
      <DATUSR>
        <CRDT>...</CRDT>
        <PSTGE>...</PSTGE>
        <PURCHASE>...</PURCHASE>
      </DATUSR>
    </USRLST>
  </HEADER>
  <ITMLST>
    <ITMDAT>
      <ITM010>...</ITM010>
    </ITMDAT>
  </ITMLST>
</ECEDOC>
```

`ITMDAT` / `ITM010` repeats for line items.

## Important sections

| Section | Purpose |
| --- | --- |
| `PROLOG` | Message type and version. Order messages use type `ON` and version `01`. |
| `HDR010` | Order header, original order references, totals, order type, retry flags, account/store data. |
| `HDR020` | Shopper information. |
| `HDR030` | Bill-to information. |
| `CRDT` | Payment / account values. |
| `PSTGE` | Shipping references and totals. |
| `PURCHASE` | Purchase/reference data. |
| `ITM010` | Line item product, price, quantity, course, rental, shipping, and idempotency fields. |

## Fixed-width editing rules

- Preserve spacing in `PROLOG`, `HDR010`, `HDR020`, `HDR030`, `CRDT`, `PSTGE`, `PURCHASE`, and `ITM010`.
- Do not pretty-print ECEDOC XML inside an SNS envelope `Message`.
- Do not add line breaks inside fixed-width fields.
- Prefer `scripts/generate_sam_payloads.py` or known-good fixtures over manual edits.
- For line-targeted physical refunds, use one refund line per payload unless intentionally testing a rejected/legacy pattern.

## Identity fields

- `SrcTransID` is the line-level idempotency key.
- `OrdRefNo` is the order-level correlation key.
- Physical refunds may target `orig_ord_ref_no` as `<shopify_order_id>-<line_no>`.
- Physical line targeting also depends on original line identifiers such as OLID and line number.

## Legacy outbound order status shape

The outbound SAM order status document describes status updates in the same XML-buffer style:

```xml
<ECEDOC>
  <PROLOG>...</PROLOG>
  <HEADER>
    <HDR010>...</HDR010>
    <USRLST>...</USRLST>
  </HEADER>
  <ITMLST>
    <ITMDAT>
      <ITM010>...</ITM010>
    </ITMDAT>
  </ITMLST>
</ECEDOC>
```

Outbound order-status examples include statuses such as shipped/posted, cancelled, backorder, and refunded. Check current code before assuming every status in the source specs is implemented.

## Workbook coverage

The prior SODA message-format workbook contained these relevant sheets:

- `OutboundOrder`
- `InboundNotification`
- `Keys`

Use current field mappings and parser code for exact implemented offsets:

- `digital_fulfillment/sam_integration/field_mappings.py`
- `digital_fulfillment/sam_integration/parsers.py`
- `digital_fulfillment/sam_integration/payload_generation.py`

## Agent rules

- Use `docs/workers-and-messages.md` for send-shape routing.
- Use `docs/payloads/REGISTRY.md` for generation and QA policy.
- Do not use stale working notes as implementation truth.
- Inspect current parser, classifier, generator, and tests before changing behavior.

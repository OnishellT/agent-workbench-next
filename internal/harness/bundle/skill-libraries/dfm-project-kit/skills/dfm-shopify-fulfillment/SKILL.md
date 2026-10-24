---
name: dfm-shopify-fulfillment
description: Use for Shopify paid orders, webhooks, product lookup, fulfillment requests or responses, fee grouping, move or split behavior, refunds, cancellations, audit claims, or Shopify payload shapes.
disable-model-invocation: true
---

# DFM Shopify and fulfillment

## Read first

- `docs/architecture.md`
- `docs/data-and-state.md#fulfillment-audit-status`
- `docs/workers-and-messages.md`
- `.github/instructions/shopify-review.instructions.md`
- Current `shopify_core/`, `shopify_orders/`, and `shopify_fulfillments/` code and tests

## Contract checks

- Preserve nested and flat line-item shapes where Shopify APIs differ.
- Reuse shared fee helpers and constants. Keep required product and fee units paired.
- Do not create fulfillment requests for orphan fee-only units without an explicit contract change.
- Preserve Shopify order, fulfillment order, fulfillment item, and line-item identities.
- Check pagination and avoid N+1 queries in audit and fulfillment paths.
- Preserve per-unit active audit claims, stale-claim recovery, retry limits, and cross-order isolation.
- Verify product lookup order in current code before changing fallback behavior.
- Test product and fee pairing, missing fee, orphan fee, move/split, retry, refund, webhook shape, and tenant isolation only as applicable.

## Review output

Name the exact unit, identifier, branch, and operational effect. Do not request unrelated cleanup or a broad rewrite of shared fee logic.

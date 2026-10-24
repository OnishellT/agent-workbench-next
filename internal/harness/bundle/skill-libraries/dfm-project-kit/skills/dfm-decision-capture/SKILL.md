---
name: dfm-decision-capture
description: "Use to convert a specific, verified DFM discussion or approved source into a draft Business Decision Record with provenance, scope, rationale, consequences, and candidate business rules. Never marks a decision accepted or publishes externally."
---

# DFM decision capture

Turn a known decision into a reviewable draft. Do not use this skill to discover broad history; use `dfm-context-research` first when the decision is uncertain.

Read [DFM core](../dfm-core/SKILL.md), `docs/domain/glossary.md`, and `docs/domain/rules/README.md` first. Open [repository sources](../dfm-core/references/repository-sources.md) only when code and historical sources diverge; [evidence](../dfm-core/references/evidence.md), [decision context](../dfm-core/references/context.md), and [trust and data](../dfm-core/references/trust-and-data.md) while evaluating source material; [mutations](../dfm-core/references/mutations.md) before creating a local draft; the [BDR template](references/bdr-template.md) only after the domain-modeling gate passes; and [receipt](../dfm-core/references/receipt.md) only before final output.

## Domain modeling gate

Before drafting a BDR:

1. Compare the discussion terms with the glossary. Name overloaded or conflicting terms and propose one precise canonical term.
2. Test the proposed decision with concrete DFM scenarios. Cover the boundaries that could change its meaning, such as tenant, environment, order family, fulfillment mode, retry, partial outcome, or external-system state.
3. Compare the stated behavior with current code and tests. Report whether code matches, lags, or contradicts the proposed rule.
4. Create a BDR only when the choice is hard to reverse, surprising without context, and the result of a real tradeoff.

If the choice fails the BDR gate, do not manufacture a decision record. Return the smaller artifact it needs: a glossary change, candidate business rule, Jira clarification, or no durable record.

## Procedure

1. Require concrete source references: complete Slack thread, approved document, Jira decision, meeting record, or equivalent.
2. Read the complete relevant context. Do not extract a decision from one quoted message when replies may qualify or reverse it.
3. Separate context, proposals, objections, explicit decision, exceptions, rationale, and unresolved questions.
4. Run the domain modeling gate and record the scenario results.
5. Search existing BDRs and BRs for duplicates, dependencies, overlap, contradiction, and supersession.
6. Define temporal and domain scope: effective dates, environments, services, entities, flows, and exceptions.
7. Draft status as `candidate` or `proposed`. Human or domain-owner review promotes it to `accepted`.
8. Cite stable source metadata. Copy sensitive source text only when its visibility permits it and the excerpt is necessary.
9. Derive glossary changes and candidate business rules separately. They are review suggestions, not authoritative updates.
10. Write one BDR per decision. Split unrelated decisions rather than creating a meeting-summary record.
11. Validate the draft against the template and show the diff.

## Output contract

The BDR draft, glossary proposal, or candidate business rules are the detailed artifact. Do not duplicate their bodies in chat.

Return one DFM receipt as the final response. Include the gate result, artifact path and status, decision sentence when warranted, canonical-term conflict, scenarios checked, code or test drift, missing authority, and artifact URI.

Local draft creation is M1. Do not post to Slack, edit Jira, publish documentation, or approve the decision.

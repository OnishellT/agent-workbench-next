# DFM decision records

This directory stores reviewed Business Decision Records. A BDR explains what was decided, why, its scope, and which sources support it.

Slack, Jira, documents, PRs, code, and tests are evidence. A discussion is not automatically a decision. Current code proves mechanics, not intent.

## Status

```text
candidate -> proposed -> accepted -> superseded
                       -> rejected
                       -> deprecated
```

An agent may draft `candidate` or `proposed`. It must not mark a decision `accepted` without an approved source or explicit domain-owner review.

## BDR gate

Create a BDR only when all three conditions hold:

1. The choice is costly to reverse.
2. A future engineer would find it surprising without context.
3. Real alternatives existed and the decision accepted a tradeoff.

Test the decision with concrete scenarios before drafting it. If it fails the gate, use a glossary update, candidate business rule, Jira clarification, or no durable record.

## One decision per file

Name records `DFM-BDR-<sequence>-<slug>.md`. Use [TEMPLATE.md](TEMPLATE.md). Split unrelated decisions. Keep alternatives and exceptions with the decision they qualify.

## Source handling

Record stable links, IDs, dates, revisions, and visibility. Do not copy private Slack or customer data into the repository. Use a redacted summary or source-only reference when the source audience is narrower than the repo audience.

## Relationship to business rules

Accepted decisions can support current rules under `docs/domain/rules/`. A rule states what must hold now. A BDR preserves the reason and history. Superseding a decision does not delete it.

## Retrieval

Normal workflow skills should load only the relevant rule and decision summary. Read a full BDR when rationale, alternatives, exceptions, or historical behavior affects the task.

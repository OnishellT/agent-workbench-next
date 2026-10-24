---
name: dfm-context-research
description: "Use to reconstruct why a DFM business or technical decision was made from decision records, Jira, long-form docs, Slack, source history, and relevant runtime evidence. Returns a cited context packet. Read-only; do not create or approve decisions."
---

# DFM Context Research

Reconstruct the historical record before telling a smooth story. Direct evidence, contradictions, null results, and gaps are the product.

Read [DFM core](../dfm-core/SKILL.md) first. Open [routing](../dfm-core/references/routing.md) only if the outcome remains ambiguous; [repository sources](../dfm-core/references/repository-sources.md) when multiple checkouts or historical branches matter; [evidence](../dfm-core/references/evidence.md) when classifying and citing claims; [decision context](../dfm-core/references/context.md) when building the context packet; [trust and data](../dfm-core/references/trust-and-data.md) before handling private or external text; and [receipt](../dfm-core/references/receipt.md) only before final output.

## 1. Pin the question

State the target, time window, entities, environment if relevant, and the exact "why" question. If the referent is inferable from the active ticket, PR, file, or conversation, state the interpretation and proceed.

## 2. Establish anchors

Collect the smallest stable anchor set: ticket keys, PRs, files/symbols, commits, domain entities, people, dates, and likely channels/documents. Do not search a whole workspace without anchors.

## 3. Build a coverage map

Search every available, relevant category independently:

1. accepted BDRs, active BRs, and glossary;
2. Jira issues, parents, links, and relevant comments;
3. current project docs plus the pinned ATL and handbook branches when available, using their catalogs and narrow domain skills before opening source material;
4. complete Slack threads, not isolated snippets;
5. source control, PR discussion, tests, and code comments;
6. runtime, incident, or analytics evidence only when the question concerns observed behavior or a threshold.

Run independent searches concurrently when the host supports it. Record unavailable and empty categories; do not hide them.

## 4. Classify evidence

For discussion sources, distinguish question, proposal, support, objection, tentative agreement, explicit decision, exception, reversal, implementation evidence, and unresolved point. Code proves mechanics, not intent. A Slack agreement is not automatically an accepted decision.

Compare source terms with `docs/domain/glossary.md`. Report overloaded words, conflicting definitions, and code-language drift. Keep this skill read-only; route an approved terminology or decision change to `dfm-decision-capture`.

Recalled Mnemopi content may supply search anchors. It is not a consulted source, must not be cited, and cannot resolve a contradiction without current evidence.

Surface competing explanations. Spot-check citations before finalizing.

## Output contract

Write the complete cited context packet to one `local://` artifact when the source set, competing explanations, or chronology does not fit the receipt.

Return one DFM receipt as the final response. Include the direct conclusion, strongest source references, material contradiction or unknown, source coverage gaps, the artifact URI when created, and `dfm-decision-capture` only when an unrecorded decision is explicit enough to draft. Do not retell the full research history in chat.

Do not write a BDR, change Jira, post to Slack, modify code, or perform environment actions.

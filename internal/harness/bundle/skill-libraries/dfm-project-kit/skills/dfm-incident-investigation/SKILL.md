---
name: dfm-incident-investigation
description: "Use for read-only diagnosis of a concrete DFM order, refund, status, price, notification, SAM/Shopify integration, admin, or environment symptom. Reproduce, trace, and identify root cause. Do not implement fixes, resend, reprocess, restart, or mutate data."
---

# DFM incident investigation

Reproduce first. Trace the earliest incorrect state. Stop at a supported root cause or an explicit evidence gap.

Read [DFM core](../dfm-core/SKILL.md) and the matching domain skill first. Open [repository sources](../dfm-core/references/repository-sources.md) only when historical branches or checkouts matter; [decision context](../dfm-core/references/context.md) when the rule changed over time; [evidence](../dfm-core/references/evidence.md) before assigning root-cause confidence; [trust and data](../dfm-core/references/trust-and-data.md) before handling external or private material; [mutations](../dfm-core/references/mutations.md) only when a proposed probe exceeds M0; and [receipt](../dfm-core/references/receipt.md) only before final output.

## Scope

Establish expected behavior, observed behavior, environment, relevant ticket/PR, identifiers, and time window. Resolve missing values from available evidence. Ask one focused question only when environment or target ambiguity prevents safe investigation.

Select the matching domain skill. Use handbook debugging recipes, status maps, and message contracts as hypothesis guides only. Verify every path and expected state against active code and tests.

## Feedback loop

For a hard, recurring, intermittent, or performance bug, establish one red-capable loop before settling on a root cause. Prefer:

1. an existing focused test;
2. a CLI or management command with synthetic input;
3. an API request against an authorized local environment;
4. a browser reproduction in an authorized environment;
5. a redacted captured payload or trace replayed locally;
6. a temporary script outside tracked files;
7. a differential or bisect command.

The loop must exercise the reported symptom, not a nearby failure. Record the command, expected signal, observed signal, determinism or reproduction rate, and runtime. Minimize the input and steps while the loop stays red.

This skill remains M0. Use existing code, tests, debugger, logs, or temporary files. If diagnosis needs source instrumentation, production access, or another mutation, state the exact probe and required workflow. Do not call an untested theory a root cause.

## Procedure

1. Pin current Jira, code or PR revision, environment identity, and relevant runtime state.
2. Resolve a small decision context packet for the entities and incident time. Distinguish the rule then from the rule now.
3. Establish the best transaction and correlation keys before tracing records across systems.
4. Build or select the feedback loop. Reproduce the exact symptom and minimize it when the bug warrants a loop.
5. Build a compact event and state-transition timeline.
6. Locate the failing boundary before searching broadly: ingress/payload, parsing/classification, service/builder, external mutation, fulfillment, refund/status mapping, notification, admin/UI, or infrastructure/permissions.
7. Trace backward from the symptom to the earliest incorrect state.
8. For a hard bug, form three to five ranked hypotheses. Give each a falsifiable prediction and run one-variable probes. For a simple deterministic bug, one directly falsified hypothesis is enough.
9. Prefer debugger or REPL state over added logs. Use targeted existing logs next. Do not collect everything and search afterward.
10. Separate root cause, contributing conditions, and downstream symptoms.
11. Re-run the feedback loop or decisive probe before assigning confidence.
12. Assess affected scope and propose the smallest remediation and regression seam.

## Output contract

Write a long timeline, hypothesis table, or evidence inventory to one `local://` artifact.

Return one DFM receipt as the final response. Include expected versus observed, reproduction or decisive probe, root cause and confidence or leading hypothesis, affected scope, smallest remediation seam, material evidence gaps, the artifact URI when created, and the next implementation or TDD capability when warranted. Do not narrate every probe.

Remain M0. Do not edit code, push, change Jira, restart services, resend notifications, repair data, rotate credentials, or communicate externally.

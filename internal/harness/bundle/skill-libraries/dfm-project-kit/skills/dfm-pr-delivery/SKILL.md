---
name: dfm-pr-delivery
description: Use when verified local DFM work must be committed, pushed, or opened as a reviewable PR. Check scope and secrets, preserve reviewable history, write an evidence-based PR description, publish only the authorized steps, inspect initial CI, and stop before merge or deployment.
---

# DFM PR delivery

Deliver verified work. Do not use delivery to finish implementation, hide scope, or merge the result.

Read `dfm-core`, the implementation or remediation receipt, the governing Jira ticket, and the active domain skill first.

## Select a mode

- `PREPARE`: review the local diff, commit plan, and PR content without remote writes. M1.
- `PUBLISH`: perform the exact authorized commit, push, and PR actions. M2.
- `STATUS`: inspect the current PR and CI without changing them. M0.

A request to prepare a PR does not authorize push. A request to push does not authorize PR creation, Jira updates, merge, tag, or deployment unless each action is explicit.

## Delivery gate

Require:

- clean knowledge of base branch and current HEAD;
- a DFM Receipt from implementation, remediation, or plan verification;
- focused verification results;
- no unresolved blocking finding;
- no secrets, customer data, environment payloads, or unrelated files;
- a ticket and scope that match the actual diff.

If a shared contract changed, require a current `dfm-blast-radius` report. If an approved plan exists, require `dfm-plan-verify` before publication.

## Make the change reviewable

1. Inspect status, diff, commits, generated files, migrations, and lockfiles.
2. Separate unrelated changes before delivery.
3. Keep mechanical and behavioral changes distinct when that helps review.
4. Follow the repository's commit convention. Do not impose a foreign convention.
5. Do not rewrite published history unless the user requested cleanup and approved the exact rewrite.
6. Before any rewrite, preserve the original tip and tree. Verify the resulting tree still carries the intended content.
7. Recommend splitting when one PR contains independent behavior that cannot be reviewed as one unit.

## Write the PR

Use facts from the ticket, diff, receipts, and verification. Include only non-empty sections:

```text
## Why
The requested behavior and reason for this approach.

## Scope
Changed behavior, important paths, and explicit exclusions.

## Tradeoffs
Only real choices and costs.

## Blast radius
Affected systems and the proven or unproven safety facts.

## Verification
Exact checks and observed results.
```

Link Jira and decision sources without copying their full text. Name migrations, rollout constraints, manual QA, and residual risk when present. Do not use generic summary or test-plan filler.

## Publish

For `PUBLISH`:

1. Re-read local HEAD, remote branch, target base, and authorization.
2. Create only the approved commits.
3. Push to the exact branch. Use `dfm-git-pr-sync` when a rewrite or conflict is required.
4. Create or update the PR with the prepared title and body.
5. Re-read the PR URL, number, head SHA, base SHA, draft state, and checks.
6. Inspect the initial CI state. Classify failures, but route fixes to `dfm-pr-remediation`.

Do not enter a polling loop. Do not merge, arm merge-when-ready, tag, deploy, update Jira, or post Slack messages.

## Output contract

Keep the prepared PR body in its target artifact or provider field. Store a long commit plan or publication log in `local://`.

Return one DFM receipt as the final response. Include the mode, branch and SHAs, scope and secret checks, completed commits, push or PR result, initial CI state, residual risk, human action, and artifact or PR URI. Do not repeat the PR body.

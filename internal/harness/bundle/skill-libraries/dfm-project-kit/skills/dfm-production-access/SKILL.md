---
name: dfm-production-access
description: Use only when the user explicitly requests DFM production ECS inspection, approved AWS SSO setup, or verification of the production access runbook. Read-only by default.
disable-model-invocation: true
---

# DFM production access

Read [DFM core](../dfm-core/SKILL.md) first. Open the [DFM receipt](../dfm-core/references/receipt.md) only before reporting an inspection.

## Read first

- Approved production runbook from the team's restricted documentation
- Current approved utilities repository documentation when helper behavior matters
- `docs/source-status.md`

## Boundary

- Do not connect to production unless the request explicitly names production inspection.
- Reading production does not authorize writes, replay, reprocess, restart, deploy, secret changes, or data repair.
- Re-read the target environment, account, cluster, service, task, and container before connecting.
- Do not commit AWS config, SSO caches, credentials, shell history, copied utilities, production output, or customer data.
- Prefer stable identifiers and bounded queries. Redact output before sharing.

## Procedure

1. Verify that the runbook and helper paths still exist.
2. Authenticate through the approved SSO flow.
3. State the read-only question before opening a shell.
4. Run the smallest query that answers it.
5. Exit the shell and record evidence without secrets.

If the user requests a production mutation, stop after the read-only evidence and require the dedicated high-risk workflow and exact authorization.

## Output contract

Return one DFM receipt as the final response. Include the verified account, region, cluster, service, task or container needed to reproduce the read, the bounded finding, redaction performed, unresolved access gap, and an artifact URI when one was created. Include `External actions` only for attempted or completed access operations. Do not paste production output.

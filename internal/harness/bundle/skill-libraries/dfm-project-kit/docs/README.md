# DFM agent documentation

Start here after `AGENTS.md` and `.atl/skill-registry.md`. This directory is a short map of the current system. It does not replace code, tests, Jira, PRs, or detailed contracts.

## Reading protocol

1. Identify the workflow skill and DFM domain.
2. Use the table below to choose one document.
3. Search that document for a heading, symbol, status, queue, or entity.
4. Read only the matching section and its cited code paths.
5. Open a deep reference only when the task needs exact fields or history.
6. Verify behavior against the active main revision or PR head.

Do not read every file in `docs/`. Choose one document, search for the relevant heading, and read that section.

## Question map

| Question | Read | Then inspect |
| --- | --- | --- |
| What is DFM and where does a flow live? | [Architecture](architecture.md) | The named app and active domain instructions |
| Which model or status owns this state? | [Data and state](data-and-state.md) | The cited model, service, and tests |
| Which worker consumes or publishes this message? | [Workers and messages](workers-and-messages.md) | The management command and settings |
| How do I run or test the project? | [Development and tests](development-and-tests.md) | The active `Makefile`, `pyproject.toml`, and CI |
| How do I trace an order or failure? | [Debugging](debugging.md) | Current models, admin, lifecycle code, and logs |
| What does a DFM term mean? | [Glossary](domain/glossary.md) | The linked model or contract |
| Why was a behavior chosen? | [Decision records](decisions/README.md) | Jira, Slack, Git history, and current code |
| Is this documentation current? | [Source status](source-status.md) | `.atl/source-revisions.json` and current Git refs |

## Deep references

Use these only after the short map identifies the needed topic:

- `contracts/` for exact external contract extracts.
- `payloads/REGISTRY.md` for generator types and payload policy.
- `digital_fulfillment/sam_nextgen_integration/OPERATIONS.md` in the active codebase for the NextGen outbox rollout and recovery contract.
- The pinned handbook branch for historical architecture, debugging, and operations context.

---
name: dfm-local-development
description: Use for DFM setup, current Make targets, Python tooling, focused tests, local workers, payload generation, environment debugging, or CI parity questions.
---

# DFM local development

Read [DFM core](../dfm-core/SKILL.md) first. Open the [DFM receipt](../dfm-core/references/receipt.md) only when the work produces verification, findings, an artifact, or a handoff.

## Read first

- `docs/development-and-tests.md`
- Active `Makefile`
- `pyproject.toml`
- `.github/workflows/ci.yml` when CI parity matters

## Rules

- Prefer current Make targets. They load the expected environment and virtualenv.
- Use `make djangotest TEST_TARGETS="..."` for focused Django tests.
- Use `make runchecks` at a broad verification boundary, not after every edit.
- Current main uses Python 3.13, Django 5.2, Ruff, Pyright, mypy, Bandit, and coverage. Verify exact versions in the active files.
- The local server uses port 8005 through `make runserver`.
- A local worker can target shared AWS queues. Verify URL, account, region, tenant, and side effects before starting it.
- Generate one-off payloads into a scratch path. Do not commit real user, customer, credential, or access-link data.

## Output

Answer a simple command or setup question directly in 200 tokens or less. Cite the active target and defining file, and name any CI difference.

After executed checks or environment diagnosis, return one DFM receipt as the final response with the command, observed result, blocker or risk, and an artifact URI when one was created. Do not add a separate setup summary.

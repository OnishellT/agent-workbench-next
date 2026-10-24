# DFM development and tests

Verify every command in the active `Makefile`. The docs branches can lag behind main.

## Runtime and tooling

Current main uses Python 3.13.3 and Django 5.2. Ruff owns formatting, import sorting, and lint checks. Pyright and mypy provide separate type checks. Bandit handles the repository security scan. Django tests and coverage use `digital_fulfillment.test_settings` by default.

The local server listens on `127.0.0.1:8005` through `make runserver`.

## Common commands

```bash
make init
make migrate
make runserver
make format
make format-check
make runchecks
make test
make djangotest TEST_TARGETS="sam_integration.tests.test_name"
```

Use `TEST_SETTINGS=<settings_module>` when the task needs another test database configuration. CI uses `digital_fulfillment.postgres_test_settings` for its PostgreSQL run.

`make runchecks` currently runs Ruff formatting and lint checks, Pyright, Bandit, and tests with coverage. It is a broad boundary check. Use a focused Django target while iterating.

## Test locations

Tests live beside each Django app:

```text
api/tests/
coesp_client/tests/
sam_integration/tests/
sam_nextgen_integration/tests/
shopify_core/tests/
shopify_orders/tests/
shopify_fulfillments/tests/
smart_license/tests/
utils/tests/
```

Observability templates have a separate unittest suite under `ops/order-processing-observability/tests/`.

## What to test

Choose scenarios from the changed contract. Common high-risk areas are:

- parsing, validation, and classifier branches;
- new, duplicate, retry, and idempotent replay;
- digital, physical, rental, pickup, ship-to, and multi-line behavior;
- refund, cancel, partial, and mixed line outcomes;
- Shopify product and fee grouping;
- fulfillment move, split, claim, and retry behavior;
- COESP transient versus fatal errors;
- Smart License activation and redaction;
- outbox claim, retry, stable payload, and duplicate delivery;
- API response keys, casing, auth, and status semantics;
- tenant isolation and sensitive logging.

Request exact assertions. "Add more tests" is not a review finding.

## CI

Current CI has separate work for:

- observability asset validation;
- Ruff lint and format plus mypy;
- Bandit;
- PostgreSQL tests with coverage;
- additional build and dependency checks defined in the workflow.

Read `.github/workflows/ci.yml` for current jobs. Do not infer CI parity from `make runchecks` alone.

## External systems

Tests should patch Shopify, COESP, SNS, SQS, and other external calls unless the task explicitly requires an integration environment. A local worker can consume a shared queue. Verify queue URL, account, region, tenant, and allowed side effects before execution.

## Payload fixtures

Use `docs/payloads/REGISTRY.md` to identify the message family and current generator path. Generate scenario variants into a scratch directory. Do not commit real customer, QA user, credential, or access-link data.

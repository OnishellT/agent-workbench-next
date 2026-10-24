:sh pandoc %{file_path_absolute} -o /tmp/helix-preview.html && xdg-open /tmp/helix-preview.html'run-shell-command': unknown variable 'file_path_absolute'Digital Fulfillment Manager

DFM is a Django service that coordinates SAM order intake, Shopify order and fulfillment work, COESP provisioning, Smart License activation, HTTP APIs, and AWS message processing.

Current architecture and agent documentation start at [docs/README.md](docs/README.md).

## Local start

Use the active Make targets. They create or load the expected environment and virtualenv.

```bash
make init
make migrate
make runserver
```

The local server listens on `http://127.0.0.1:8005`.

Run a focused test while editing:

```bash
make djangotest TEST_TARGETS="sam_integration.tests.test_name"
```

Run the broad local check at the verification boundary:

```bash
make runchecks
```

See [development and tests](docs/development-and-tests.md) for current tooling, test layout, CI differences, workers, and payload generation.

## Documentation map

| Need                                          | Read                                                   |
| --------------------------------------------- | ------------------------------------------------------ |
| System boundaries and apps                    | [Architecture](docs/architecture.md)                   |
| Models, IDs, statuses, and outbox             | [Data and state](docs/data-and-state.md)               |
| Workers, queues, topics, and message families | [Workers and messages](docs/workers-and-messages.md)   |
| Commands, tests, and CI                       | [Development and tests](docs/development-and-tests.md) |
| Incident tracing                              | [Debugging](docs/debugging.md)                         |
| Terms                                         | [Glossary](docs/domain/glossary.md)                    |
| Decisions and current rules                   | [Decision records](docs/decisions/README.md)           |
| Source freshness                              | [Source status](docs/source-status.md)                 |

Use [docs/contracts](docs/contracts/README.md) and the [payload policy](docs/payloads/REGISTRY.md) only when the task needs exact fields or test-data rules.

## Agent entry point

Agents start with `AGENTS.md`, then `.atl/skill-registry.md`. They select one workflow skill and one domain skill before reading detailed sources. The registry points to exact documents and code paths.

Do not load all of `docs/`. Choose one document, search for the relevant heading, and read only that section.

## Quality and safety

Current main uses Python 3.13, Django 5.2, Ruff, Pyright, mypy, Bandit, Django tests, and coverage. Verify versions in `.python-version`, requirements, `pyproject.toml`, `Makefile`, and CI.

A local worker can target shared AWS queues. Confirm environment, account, region, tenant, queue or topic, and side effects before starting one. Do not commit credentials, real customer payloads, private Slack text, access URLs, passcodes, or production output.

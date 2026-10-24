# Workbench for Pi

Pi extension (same shape as LCU's `adapters/pi`) that lets the agent operate a running
Agent Workbench through its HTTP API, logging in with the local access token.

## Install

```sh
pi install /path/to/agent-workbench-next/adapters/pi     # add -l for this project only
```

Configuration (environment of the Pi process):

| Variable | Default | Meaning |
| --- | --- | --- |
| `WORKBENCH_URL` | `http://127.0.0.1:18888` | Must equal the server's `--public-url` (the API checks `Origin`). |
| `WORKBENCH_TOKEN_FILE` | `${XDG_STATE_HOME:-~/.local/state}/agent-workbench-next/access.token` | Token file written by the Workbench binary. Read only to log in; never shown to the model. |

## What it adds

- Tool `workbench_api` `{method, path, body?}`: any `/api/*` route (hosts, conversations,
  tasks, diagnostics) and Foundry's own `/foundry/api/*`. `GET` runs directly; `POST/PUT/DELETE` ask you to approve
  in Pi's UI and are blocked in headless mode.
- `/workbench status` (health + unmounted modules), `/workbench open` (URL), `/workbench login`.

The session cookie is random and lives in Workbench's memory; after a Workbench restart the
client logs in again on the next call.

## Test

```sh
cd adapters/pi && bun test
```

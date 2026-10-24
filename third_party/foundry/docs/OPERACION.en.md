# Operation

[English](OPERACION.en.md) | [Español](OPERACION.md)

## States and expectations

Capture detects completion signals and proposes tasks against a complete conversation revision. **It does not confirm that they are finished.** There are three independent human decisions:

1. Confirm completion: `episode.complete` with `id` and `completed: true`. The task becomes completed and remains unrated.
2. Assess the outcome: `episode.label` with `label: correct` or `incorrect`, after confirmation. The second option learns what to avoid; both paths require evaluation. `pending` retains completion but withdraws the assessment.
3. Approve adoption: after an eligible experiment, `candidate.promote` with `id` and `experiment_id`. No setting enables automatic promotion.

The system preserves revisions, confirmations and labels. Reopening with `completed: false` resets the assessment to pending and invalidates derived jobs/evidence. A correct label does not prove every step was useful. Extraction produces a hypothesis with positive and preventive formulations and evidence references.

Jobs are queued and can be cancelled. A process interruption preserves history; interrupted jobs are shown for explicit retry. Retrying an inference operation may consume quota again. The dashboard displays errors and decision reasons without treating infrastructure errors as evidence of improvement.

Promotion checks the experiment's evidence and configuration against the current state. If you change labels, cases, policy or harness, evaluate again. Synthetic proposals do not satisfy the real-inference requirement. General improvements need domain variety; repeating a single task does not establish generalization.

## Configure providers

The extraction provider reads evidence and returns a structured proposal. The replay provider modifies a disposable workspace to solve a task. You can configure each independently in Settings.

**Pi:** uses an authenticated Pi installation, normally with command `["pi"]`, model provider `openai-codex`, model `gpt-6.1-sol` and reasoning `medium`. Analysis runs in an empty temporary directory without tools, extensions, MCP, skills, context files or persistent sessions. Foundry requests JSON and validates the response locally; it does not claim that Pi enforces a schema during generation. Replay retains built-in tools and operates within the case workspace, while keeping extensions and inherited sessions disabled.

The CLI configures analysis and replay independently, without running inference:

```bash
conversation-foundry configure --provider pi --provider-id openai-codex \
  --model gpt-6.1-sol --reasoning medium --target analysis
```

`--target analysis` is the default and preserves replay settings. Use `--target replay` to change only the evaluation agent or `--target both` to explicitly apply the changes to both. Switching between Pi and Codex selects the corresponding executable unless you specify `--agent-command`. Pi requires a single executable in that array; switching to the `command` adapter requires you to provide its argument array explicitly.

**Codex:** install and authenticate the CLI using the agent's own mechanisms. Foundry does not store API keys. Configure the command as a list, normally `["codex"]`, and optionally select a model. Diagnostics check the executable; authentication is confirmed when making a call.

**Command:** supports adapters for other agents. It runs without a shell, with the workspace as its current directory and this JSON object on stdin:

```json
{"schema_version":1,"prompt":"...","workspace":"...","output_schema":null}
```

It must return exactly one JSON object on stdout:

```json
{"text":"final response","usage":{"input_tokens":120,"output_tokens":80},"turns":1,"tool_calls":3,"model":"model-identifier","real_inference":true}
```

For extraction, `text` contains JSON conforming to `output_schema`. Adapter logs go to stderr. For replay, the adapter must perform the work in `workspace`. Merely describing a solution in its response is insufficient.

`turns` and `tool_calls` are optional nonnegative integer metrics. Turns are completed provider turns, not tool calls or internal reasoning steps. Omitted metrics are shown as unavailable; zeros are not invented. The Codex adapter sums usage from `turn.completed` events and counts observed tools separately.

Enable **Adaptador command con inferencia real** (Command adapter with real inference) only when that adapter actually calls the model. The gate requires both this setting and `real_inference: true` in its response. This declaration is part of the adapter contract, not independent provider attestation. Monetary costs are not invented when the adapter does not report them.

Commands are argument arrays. Shell strings, pipes and variable expansion are not interpreted automatically. If you need a workflow with multiple commands, create a versioned script and execute it.

## Verifiers

Create verifiers in a directory outside the initial project. Foundry freezes a copy and records its hashes. Use commands such as:

```json
["{python}", "{verifier}/verify.py", "{workspace}"]
```

The positive control prepares a known solution. The verifier must return 0. The negative control introduces a defect and returns 0 if preparation succeeded; the same verifier must then return a nonzero exit code. Execution problems are distinguished from functional failures.

Design real domain controls: HTTP interfaces, persistence after restart, transactions, CLI output, exported files or browser behavior. The verifier must measure the observable result and remain independent of the agent proposing the improvement.

Processes run with the local user's permissions. Separate directories and hash checks protect against accidental changes; they are not a security boundary against hostile code. External services used by a case need an explicit test environment. Nix can pin a case's tools and services when installed.

## Access and diagnostics

The server listens on loopback and API calls require `X-Foundry-Token`. Open the link from `start` or `open`; `status` reports service state without displaying the token. When the dashboard opens, the token moves from the URL fragment to `sessionStorage` and is removed from the visible address. Do not share that private link.

- If the UI does not appear, run `conversation-foundry status` and `doctor`; check the port.
- If there are no conversations, confirm the source is enabled and click **Escanear ahora** (Scan now). Inspect its error and the session's coverage.
- If extraction fails, check the provider executable, authentication, model and timeout.
- If case creation fails, review positive and negative controls. Do not disable the verifier to make it pass.
- If promotion is blocked, read the experiment's reasons and check that the evidence remains current.
- After moving a package, reconfigure source paths and any commands using absolute locations from the previous machine.

## Connect the active harness to an agent or RLM

The accepted registry is queried by domain and project. In an RLM loop, run this at the start of a task and after promotion:

```bash
conversation-foundry context --domain python-http --project /path/to/project
```

The agent adapter must incorporate the returned context into its working instructions and load relevant skills. Only capabilities from the active version are queried; pending proposals remain outside the context. When working directly in a project, you can materialize the integration with:

```bash
conversation-foundry sync --project /path/to/project --domain python-http
```

The integration skill distributed with Foundry instructs the agent to query the active context. The RLM remains responsible for calling the provider and executing tools; Foundry retains evidence, evaluates comparisons and determines the active version. The domain is an operational identifier, such as `python-http`: it must be consistent across cases, candidates and context queries.

In **Aprendizajes** (Learnings), you can adjust the domain and scope of a proposed candidate or one whose evidence is stale. This invalidates previous results; evaluate again before promotion. A general label does not replace evidence of transfer across domains.

## Mining with skill-extractor

Extraction uses the [skill-extractor](https://github.com/surenode-ai/skill-extractor) engine (Apache-2.0, vendored unmodified under `foundry_loop/vendor/skill_extractor`) through its library API: Foundry translates the task's event range into the extractor's canonical record shape, the extractor reconstructs, renders, redacts and scores, and `foundry_loop/miner.py` maps every mined skill back into the validated candidate contract (`learning.validate_spec`). Foundry keeps the provider call, the budget, the revision binding, the label fences and schema validation. The provider receives the miner prompt plus a Foundry contract: a single `{"skills": [...]}` object, verbatim quotes as evidence and a preventive formulation. Any quote that does not occur inside the task's event range is rejected as fabricated or discarded evidence; a candidate whose quotes do not resolve is not stored.

Each candidate keeps `category` (technique, workflow, preference, guardrail, automation, domain), `trace_outcome`, `confidence`, `utility`, `evidence_range`, the `analysis_coverage` receipt and `risk`: the extractor's risk-lint findings (for example `pipe-to-shell` or `credential-access`). A flagged candidate is blocked from evaluation and adoption until a human acknowledges the findings with `candidate.acknowledge_risk` (`id`, `acknowledged: true`, `note`). The acknowledgement lives outside the spec with its own audit record.

Human decisions (`candidate.promote` with an optional `comment` and `candidate.reject` with a `comment`) are stored in the `decisions` table, which SQLite triggers protect against updates and deletes. Before each mining run Foundry mirrors candidates and decisions into `miner/candidates.jsonl` and `miner/decisions.jsonl` (skill-extractor's documented state files) and computes the calibration block and priors with the extractor's own functions. A recent decision appears in the next prompt with its reason; its influence on scores is capped at 5% per decision and 30% overall. No model is trained and no hosted service is called.

Rendered evidence is sent as one segment. If it exceeds `max_input_chars` the job fails with an explicit message and nothing is truncated or omitted; messages beyond the extractor's per-message caps are listed in `analysis_coverage.truncated_events`. Input coverage does not guarantee semantic understanding. Partially normalized sources are ineligible for extraction until coverage is resolved. Attachments are inert references.

## Export, inspect and import

`conversation-foundry export --id CANDIDATE` writes `exports/<id>.zip` with `manifest.json`, `review.json`, `skill/<name>/SKILL.md` (Agent Skills format) and both formulations. Before anything leaves the store the **generalize** step runs: project paths, the home directory, the user and the host become `{project}`, `~`, `{user}` and `{host}`; private miner and provider fields are dropped and evidence quotes are kept only as hashes. `review.json` lists every substitution and the tools the skill expects. The manifest states the gate status: export is not approval.

`conversation-foundry inspect PACKAGE` shows the manifest without installing. `conversation-foundry import PACKAGE --project PATH` verifies the hash of every declared file, rejects undeclared files, runs the **personalize** step (replaces the markers with this machine's paths and lists tools missing from `PATH`), requires `--acknowledge-risk` when the package carries findings, and writes `.agents/skills/<name>/` without ever overwriting an existing folder. An imported skill is an unmanaged folder: its gate status comes from another store and does not count as local adoption. Distribution of adopted versions remains `sync`, with its managed folders and marked AGENTS.md block.

## Persistent client and live work

`conversation-foundry catalog --project /path/project` presents a compact index without loading all bodies. `context --domain DOMAIN --project /path/project` resolves only accepted, current capabilities. `conversation-foundry repl` opens Python with the `foundry` client ready. Methods include `state()`, `context(domain, project)`, `call(action, **kwargs)`, `wait(job_id)`, `acall` and `await_job`. `conversation-foundry run --project /path/project --domain python-http --prompt "request"` performs a real task with active capabilities and ingests the result for review. It does not reconstruct the initial state of historical conversations: cases must be captured before work or prepared as new scenarios.

## Nix environment

`nix develop` provides Python and Git; `nix run .# -- doctor` runs the package. `flake.lock` pins nixpkgs. For verification with specific dependencies, use an argv such as `["nix", "develop", "/path/environment#", "--command", "python", "{verifier}/verify.py", "{workspace}"]`, with your own flake and a versioned lock. Positive/negative controls run with the same command. The package includes a real HTTP/SQLite example without external dependencies. Nix pins software; it does not itself reproduce external services, production data or isolation against hostile code. Define those resources in each domain's case.

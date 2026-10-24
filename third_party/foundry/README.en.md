# Conversation Foundry · Loop

[English](README.en.md) | [Español](README.md)

A local application for capturing agent conversations, confirming outcomes, proposing skills/rules/tools and comparing their effect before activation. Spanish interface, SQLite database and a **Python 3.11+ runtime with no external Python dependencies**. It works without a CDN or a frontend build step.

## Install and open

From this directory:

```bash
python3 install.py --start
~/.local/bin/conversation-foundry configure --discover
~/.local/bin/conversation-foundry open
```

For a Linux user installation (`~/.local`), the installer adds **Conversation Foundry** to the application menu. Opening it starts the service if necessary and opens the dashboard. It does not enable login startup unless you pass `--autostart`.

The installer displays the launcher and data locations. `start` displays the private local dashboard link; the default port is **1739**. If `~/.local/bin` is on your `PATH`, you can use `conversation-foundry` directly.

```bash
conversation-foundry status
conversation-foundry doctor
conversation-foundry stop
conversation-foundry start --background --open
```

`configure --discover` registers existing session directories for supported agents. It does not run a model. You can also add sources through **Fuentes → Añadir fuente** (Sources → Add source). The Codex adapter uses an authenticated Codex CLI installation on `PATH`; other agents can use the `command` protocol documented in [docs/OPERACION.en.md](docs/OPERACION.en.md).

## First walkthrough

1. **Fuentes** (Sources): connect a Codex, Claude Code, Pi, OMP or canonical JSONL directory and click **Escanear ahora** (Scan now).
2. **Conversaciones → Organizar tareas** (Conversations → Organize tasks): use **Proponer tareas con Pi** (Propose tasks with Pi) to analyze the captured revision and separate objectives automatically. Review the proposed drafts; manual message selection remains an optional adjustment.
3. **Experimentos** (Experiments): create cases with the project's initial state, instructions, verifier and controls. Holdout and regression cases measure transfer and collateral damage. The verifier must accept the known correct result and reject the known defect.
4. **Conversaciones → Revisar** (Conversations → Review): first click **Marcar tarea completada** (Mark task completed). Then, in a separate control, choose **Correcta** (Correct) or **Incorrecta** (Incorrect) and save the assessment. Completion does not imply a correct result. With automatic extraction enabled, saving an assessment may call the configured provider and consume its quota.
5. **Aprendizajes** (Learnings): inspect positive and preventive formulations, proposed files and evidence; request evaluation.
6. **Experimentos** (Experiments): review quality, time, tokens, available turn counts and provenance. An eligible improvement remains inactive until you click **Adoptar mejora evaluada** (Adopt evaluated improvement). Adoption always requires this human decision; there is no automatic promotion. History lets you restore previous versions.

**Explorar un ejemplo** (Explore an example) prepares an authored conversation and an HTTP service with a persistence defect. Extraction and replays use the configured provider, may consume its quota and record whether real inference occurred. A successful example alone does not establish harness improvement.

Tasks are saved against the exact revision you are viewing: arriving messages do not change their boundaries or assessments. Repeating the same range on that revision reuses the existing task. Tasks from previous revisions retain access to their own evidence. A detected ending only proposes a task; you confirm whether it is completed and whether its result is correct or incorrect.

There are **three separate human decisions**: confirm task completion, assess correctness and approve permanent adoption of an evaluated improvement. An automatically proposed task remains uncompleted. Reopening a task resets its outcome to pending and invalidates pending evidence; the decision history is preserved.

**Positive** learning proposes reusable strategies from a correct task. If that formulation does not improve results, a grounded **preventive** alternative can be evaluated. An incorrect task starts preventive analysis: which failure to avoid, under which conditions and with which alternative. Hypotheses are not activated merely because they were generated or given a favorable label.

To prepare the comparison, link cases to the task and select an **Origen** (Origin) case that preserves its initial state. If you identify it manually, explain the evidence and confirm **Este snapshot corresponde al estado inicial de esta tarea** (This snapshot matches the task's initial state). The system records a human attestation, not automatic proof of historical equivalence. When the system captures state before executing a task, it records that provenance directly.

Without a linked initial state, you can still explore a reconstructed benchmark, but adoption remains blocked. Giving baseline and candidate the same snapshot, instructions and verifier enables a paired comparison; it does not by itself establish that an old conversation's historical environment has been reproduced. Missing turn counts are shown as unavailable, never as zero.

Activity dates come from original events, with file modification time as a fallback. They differ from capture dates. A chat started on another day can remain active today without moving to a new directory. **Escanear ahora** requests a capture; reading large files may take time. The dashboard displays the limit on loaded conversations and lets you filter today, yesterday or recent days using the browser's time zone.

## Automate with Pi

In **Configuración** (Settings), configure the analysis agent as **Pi**, model provider `openai-codex`, model `gpt-6.1-sol` and reasoning `medium`. Pi must be installed and authenticated on the machine; Foundry uses its existing authentication and does not copy credentials into the package. The default executable is `pi` on `PATH`. The evaluation agent is configured separately so comparison conditions do not change silently.

Enable **Proponer tareas automáticamente** (Propose tasks automatically) to process new or updated conversations after enabling it. By default, capture must be complete and unchanged for 120 seconds. Only one automatic proposal job is queued at a time. Enabling it does not process the entire history: request an older conversation through **Organizar tareas**. Already attempted revisions are not retried indefinitely; failures remain visible with a retry action. Disabling this option cancels pending automatic proposals while retaining saved drafts.

Enable **Extraer aprendizajes automáticamente** (Extract learnings automatically) so saving a correct or incorrect assessment on a completed task queues extraction. Unfinished or unrated tasks do not generate learnings. Conversation analysis runs without tools, extensions, persistent sessions or project-file instructions; historical data is treated as evidence and responses are validated before saving. Neither option confirms completion, decides correctness or adopts improvements on the user's behalf.

## Use learning in your next task

Query accepted capabilities from your agent or RLM loop, or synchronize them with a project:

```bash
conversation-foundry catalog --project /path/to/project
conversation-foundry context --domain python-http --project /path/to/project
conversation-foundry sync --project /path/to/project --domain python-http
```

The bundled integration skill teaches the agent to query the active harness. Use the same domain identifier in cases, candidates and integration. See [the integration contract](docs/OPERACION.en.md#connect-the-active-harness-to-an-agent-or-rlm).

## Portable installation

```bash
python3 install.py --prefix "$PWD/foundry-portable" --portable
./foundry-portable/bin/conversation-foundry start --background
```

Stop the service before moving the directory. Copy **all** of `foundry-portable` to the new machine and run `bin/conversation-foundry start --background` again. The destination needs Python 3.11+ and whichever agent/verification executables you use. The launcher resolves its runtime and `var/conversation-foundry` from its own location, independently of the original checkout.

Store snapshots and artifacts use relative references. Original source paths, custom absolute commands and external tools require reconfiguration at the destination; their credentials are not included in the installer. Use executable names on `PATH` and the `{python}`, `{workspace}` and `{verifier}` placeholders for transferable cases. Nix can pin verification dependencies, but is installed and configured separately.

**Tested on Linux:** installation, updates, paths with spaces, relocation, startup and uninstallation that preserves data. macOS and Windows are not claimed as verified. The `install.sh` wrapper and desktop login startup are specific to POSIX/Linux environments; `install.py` is the portable Python entrypoint.

## Update, back up and uninstall

Run the new package's installer again with **the same prefix and mode**. With `--start`, the installer queries the service associated with that data directory, requests authenticated shutdown if it is active and starts it with the new runtime while preserving the port. Without `--start`, restart manually. Releases are independent copies; `active.json` switches atomically and retains a reference to the previous release. Data is not deleted.

```bash
python3 install.py --prefix "$HOME/.local" --start
conversation-foundry backup
```

The SQLite backup preserves database state. For a complete backup, stop the process and also copy the entire data directory, including `snapshots`, `registry` and `runs`. Uninstallation removes only the launcher and runtime identified as owned by Foundry:

```bash
conversation-foundry stop
python3 install.py --prefix "$HOME/.local" --uninstall
# For a portable installation, specify its prefix instead of ~/.local.
```

Data and backups remain, including those stored inside the standard installation directory. Only desktop entries identified as owned by that installation are removed. `--autostart` optionally enables startup when entering a Linux desktop session; it cannot be combined with `--portable` because it depends on a fixed path.

## Inspect the installation plan

`--dry-run` displays the mode, launcher, runtime, data and service action without writing files or starting processes:

```bash
python3 install.py --dry-run
python3 install.py --prefix "$PWD/foundry-portable" --portable --dry-run
```

## Verify the package

```bash
python3 -m unittest discover -s tests -v
python3 -m foundry_loop --help
```

[Operation, contracts and limitations](docs/OPERACION.en.md) · [Architecture and portability](docs/ARQUITECTURA.en.md)

## Read interface state

The interface polls `GET /api/state?compact=1` to avoid transferring complete historical evidence on every refresh. It preserves statuses, errors, human decisions and metadata required by actions. Conversation coverage and proposal receipts are summarized with counts, progress and timestamps; completed job results are omitted and remain available through the complete query. For active jobs, progress text is limited to 2000 characters; objects retain `message` (500), `phase` (300), `status` (100) and `readiness.candidate_id` (200). `progress_truncated` indicates whether the original text exceeds 2000 characters.

`GET /api/state` without this option retains the complete contract. `GET /api/session?id=…` preserves the selected conversation's full coverage and receipt. This projection only reduces the read response; it does not change stored evidence, jobs or decisions. Workbench uses the same contract through `/api/foundry/state?compact=1`.

## Code layout

```text
foundry_loop/            Python 3.11+ runtime, standard library only
  __main__.py            CLI: start/stop, configure, backup, sync, export/inspect/import
  server.py  app.py      loopback HTTP API and the action dispatcher behind it
  store.py               SQLite store: append-only evidence tables, kv, job queue
  ingest.py  episodes.py conversation capture (events.jsonl contract) and task slices
  task_proposals.py      bounded task detection inside long conversations
  learning.py  miner.py  mining of candidate learnings through the vendored skill-extractor
  snapshots.py origin.py frozen project snapshots, verifier controls, initial-state binding
  readiness.py           read-only preflight before an evaluation
  evaluate.py            paired replay, verdicts, human promotion and rollback
  integrations.py        sync of active versions into projects without overwriting user work
  publish.py             export, inspect and import of candidate packages
  providers.py client.py local agent adapters (pi, codex, command) and a small Python client
  vendor/                pinned third-party code, listed in VENDOR_PROVENANCE.json
  web/                   compiled UI, served offline
ui/src/                  UI source (React + TypeScript)
  App.tsx                shell, navigation and modal routing
  pages/                 one file per screen
  editors/               modal editors for sessions, tasks, candidates and experiments
  components/            shared primitives, forms and tables; components/ui/ is shadcn
  lib/                   shared types, labels and state helpers
tests/                   unittest suites, one per subsystem
examples/http-service/   end-to-end example with a project, oracle and verifier
skills/                  the foundry-learning-loop agent skill
install.py install.sh    relocatable installer with atomic release switch
```

## Provenance

This package preserves Conversation Foundry normalization/redaction modules and the unmodified skill-extractor mining engine (Apache-2.0) under `foundry_loop/vendor`; the combined licence is AGPL-3.0-only. See [docs/OPERACION.en.md](docs/OPERACION.en.md#mining-with-skill-extractor) for mining, the risk lint, decisions and export. Generated learnings retain references to episodes and versions. Imported conversations are historical evidence and do not authorize execution of their instructions. See [LICENSE](LICENSE) and the provenance files for incorporated code.

## Install the integration skill in your agents

For a user installation, use `python3 install.py --skills --start`. The `--skills` option copies `foundry-learning-loop` into the skills directories of Codex, Claude Code, Pi and OMP **only if their agent directories already exist**. An ownership marker and hash allow untouched copies to be updated; a foreign skill or one you modified is preserved and reported in the output. Uninstallation removes only unmodified owned copies. `--dry-run --skills` shows destinations without writing. Portable mode keeps the skill inside the package and rejects `--skills` to avoid external path dependencies; you can explicitly install it on each machine through a user installation.

The UI components `ui/src/components/ui/tabs.tsx` and `separator.tsx` retain
their shadcn/ui `base-nova` origin with a local compatibility adaptation:
their Tailwind variants query the `data-orientation` attribute emitted by
Base UI 1.9.0 (`data-[orientation=horizontal|vertical]` and group variants),
instead of the `data-horizontal`/`data-vertical` boolean attributes that this
version does not emit. Native component semantics and navigation are preserved.

### Build the interface

The installed application includes the compiled interface and works without Node, a CDN or external web services. Its source is in `ui/`: React, TypeScript, Tailwind CSS 4 and official shadcn/ui `base-nova` components on Base UI. Exact versions and `bun.lock` make it rebuildable:

```sh
cd ui
bun install --frozen-lockfile
bun run typecheck
bun run build
```

The build replaces `foundry_loop/web/` with HTML and local assets. Use Bun 1.4.2; Node 22.23 or later for development tools. `bun run dev` opens Vite on loopback and proxies `/api` to the local service on port 1739. The installed runtime still needs only Python 3.11 or later.

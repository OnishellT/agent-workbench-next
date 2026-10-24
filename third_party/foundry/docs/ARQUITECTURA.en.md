# Architecture and portability

[English](ARQUITECTURA.en.md) | [Español](ARQUITECTURA.md)

```text
Local adapters → normalized events → SQLite revisions
                                          ↓
                                  episodes and labels
                                          ↓
        skill-extractor (vendored) → candidate with evidence and risk lint
                                          ↓
                       snapshot + frozen oracle + controls
                                          ↓
                  baseline / positive → baseline / preventive
                                          ↓
        gate → human decision → versioned registry → export
                                          ↓
             decisions → bounded priors for the next mining run
```

The persistent controller maintains limits, states, budgets and decisions. The UI consumes a local API; it does not execute system commands directly. A configurable provider performs extraction and replay. An RLM can act as a `command` provider if it follows the stdin/stdout contract; the durable scheduler remains responsible for promotion rules. This package neither includes nor simulates Prime Intellect's RLM runtime.

## Distribution

```text
PREFIX/
  bin/conversation-foundry          relocatable launcher
  share/conversation-foundry/
    .owned                         installer ownership marker
    active.json                    active release and location settings
    releases/v1-HASH/
      foundry_loop/                runtime and offline UI
      skills/ examples/ docs/
      install.py install.sh
  var/conversation-foundry/        data, portable mode only
```

Outside portable mode, data defaults to `~/.local/share/conversation-foundry/data`. Use `--data` for an explicit location; do not combine it with `--portable`.

The installer computes the release identifier from the distributed content. It copies files into a temporary directory and then switches the active pointer atomically. The launcher finds its prefix from its own path, selects the release and executes `python -m foundry_loop`. It does not depend on the source repository or a virtual environment generated on another machine. Updates retain previous releases and the existing store; `install.py --start` authenticates to the service associated with the data directory and restarts it to load the new code. Without that option, restart the process manually. It never sends signals to a PID or kills a process occupying a port.

## Portable data and external dependencies

The store contains SQLite, snapshots, verifiers, candidates, runs and versions. Copy the whole store with the process stopped. Snapshots and internal paths resolve from their new root. Original conversation files remain external sources; the package does not copy credentials, installed agents or production services.

Executables resolve through `PATH`. `{python}` uses the current runtime's interpreter; `{workspace}` and `{verifier}` expand for each test. A case that depends on external services, binaries or paths also needs them at its destination. Nix can specify these dependencies within a case, but the installer does not download Nix automatically.

## Reversibility

Restoring a version changes the active harness and retains history. It does not overwrite external agent configuration files without an explicit integration action. Exporting a candidate produces an inspectable package and does not establish approval. Uninstallation preserves data; users manage their data directory separately to delete history.

## Design references

- [skill-extractor](https://github.com/surenode-ai/skill-extractor) (Apache-2.0, vendored): skill mining, risk lint and learning from decisions. [skill-forge](https://github.com/gzb1128/skill-forge) (MIT): paired-trial isolation rules. [skill-optimizer](https://github.com/hqhq1025/skill-optimizer) (MIT): mine/personalize/generalize split. Research: SKILL-DISCO (arXiv 2606.26669), SelfEvoSkill/PACE (arXiv 2606.14239), TRACE on verifier brittleness (arXiv 2610.11678).
- [Prime Agent](https://github.com/PrimeIntellect-ai/prime-agent): agent context and harness refinement; Foundry exposes a Python client for connecting to a loop.
- [Verifiers v1](https://www.primeintellect.ai/blog/verifiers-v1): separation of tasks, harness and execution environment.
- [Hermes skills](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/developer-guide/creating-skills.md): a compact index and loading SKILL.md, scripts and resources when relevant.
- [Nix integration tests](https://nix.dev/tutorials/nixos/integration-testing-using-virtual-machines): reproducible environments and observable behavior checks.

These are architectural references; the package does not claim equivalence with those runtimes or incorporate their remote services.

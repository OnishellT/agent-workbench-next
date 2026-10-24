# Contributing

[AGENTS.md](AGENTS.md) holds the architecture map and the hard rules; [plan.md](plan.md) is the only
source of scope. Read the relevant part of both before changing code.

## Set up

```sh
nix develop                                   # Go 1.26 + bun 1.4 pinned by the flake (or use direnv)
(cd web && bun install --frozen-lockfile && bun run build)   # the Go binary embeds web/dist
make check                                    # lint, Go tests, web tests, Foundry suites, packaging guard
```

Python 3.11+ is needed for Foundry at runtime (`python3` on PATH, or `WORKBENCH_FOUNDRY_PYTHON`).

## Branches and pull requests

- Work on a branch, open a pull request against `main`, keep it small and focused. `main` is protected:
  linear history, merged by rebase or squash, never pushed to directly.
- Fill in the pull request template. Say what changed, why, and how you checked it.
- Change behaviour and its documentation together (`docs/`, `AGENTS.md`, the ADRs). New behaviour needs
  tests that can fail, including the negative and edge cases.
- Do not edit an existing SQLite migration; add a new one. Do not touch `third_party/foundry/foundry_loop/vendor/**`.
- If you change `third_party/foundry/ui/src`, run `make foundry-ui` and commit the rebuilt bundle.

## What CI checks

Every pull request runs these; the required checks are `ci-ok`, `security-ok` and `review-gate`.

| Job | What it proves |
| --- | --- |
| `web` | typecheck, unit tests and production build of the frontend |
| `go` | gofmt, `go vet`, staticcheck, all Go tests, the Docker packaging guard, release binaries |
| `foundry (python 3.11 / 3.12 / 3.13)` | Foundry's own suite on every supported Python, pyflakes |
| `foundry-ui` | typecheck, translation-engine and string-coverage tests, committed bundle equals its sources |
| `tooling` | the reviewer and review-gate tests, the Pi adapter tests, shellcheck |
| `image` | the Docker image builds and, started for real, serves the dashboard and Foundry |
| `e2e` | real-browser specs against a freshly built instance |
| Security workflow | govulncheck, secret scan, dependency review, CodeQL |

`make check` is a subset of these: it covers lint, Go, web and Foundry tests, typecheck and the packaging guard. The tooling tests (`bun test ./scripts/review ./scripts/review-gate.test.ts`), the image smoke test (`scripts/ci/smoke-image.sh`) and the browser specs (`scripts/ci/start-trial.sh` plus `bunx playwright test`) run only in CI unless you run them yourself. Specs that need fixtures from a real machine (SSH hosts,
a signed-in Magpie) are not part of CI; run them against a trial instance when you touch that code.

## Review gate

No pull request is merged until the **local reviewer** has reviewed its final commit with confidence 4 or 5
and no actionable findings (critical, major or minor). Documentation-only pull requests, and files listed in
`.github/review.json`, are exempt.

```sh
make review PR=<number>                    # reviews the head commit and posts one review
make review PR=<number> ARGS=--dry-run     # prints the review without posting
make pr-status PR=<number>                 # checks, reviews, confidence and open threads in one screen
```

The reviewer is [`pi`](https://pi.dev) running on your machine with your own model quota
(`scripts/review-pr.ts`). It posts a single review with inline comments, a summary and a confidence score from
1 to 5 (5 safe to merge, 4 only nits, 3 issues to fix, 2 serious problems, 1 must not merge), and a hidden
marker that the required `review-gate` check reads. After posting, it re-runs the failed gate for that commit,
so the check turns green without a push.

It builds its context from `AGENTS.md`, this file, `.github/copilot-instructions.md`, the path-scoped
`.github/instructions/*.md`, the matching `.pi/skills/*`, the review config in `.github/review.json`, the pull
request text, the linked issue, the diff and every earlier thread. Rules, prompt and tool profile are read from
the **base branch**, so a pull request cannot rewrite the reviewer that judges it. It runs in a throwaway
worktree with a minimal environment, read-only tools and an empty home, and never posts anything containing a
local secret. It needs `pi` signed in to the `opencode-go` provider; change the model with `REVIEW_MODEL`.
After three rounds without converging it stops and asks for a human (`--force` overrides).

Before leaving draft, read your own diff against `.github/copilot-instructions.md`: the goal is a first review
that finds nothing.

## Releases

Tag `vX.Y.Z` on `main`. The Release workflow tests, builds the static binaries and the image, publishes the
image to `ghcr.io/onishellt/agent-workbench-next` with build provenance and creates the GitHub release with
checksums. Run the workflow by hand (without a tag) to rehearse the build.

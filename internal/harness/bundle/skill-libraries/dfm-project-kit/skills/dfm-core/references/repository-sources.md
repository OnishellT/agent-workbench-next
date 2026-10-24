# DFM Repository Sources

DFM knowledge is split across the active codebase and two documentation branches. Discover and pin each source before using it; do not assume the documentation branches are merged or current.

## Start from the active checkout

1. Record repository root, branch, HEAD, remotes, and worktree state.
2. Read `AGENTS.md` when present.
3. Read `.github/copilot-instructions.md` and the matching `.github/instructions/*.md` file for reviewed or changed domains.
4. Inspect the current `Makefile`, `pyproject.toml`, code, and tests before naming commands or behavior.

Current code/tests and the pinned PR establish current mechanics. They do not establish historical rationale by themselves.

## Historical documentation branches

The older `docs-atl-skills-cleanup` and `doc/dfm-repo-handbook` branches can explain history. Pin their revisions before use. Treat their skills, catalogs, debugging recipes, status maps, and contract notes as evidence to verify against the active checkout.

Do not copy historical indexes into the current context. Search the branch for the named entity, read the matching section, then confirm the cited path in current code and tests.


## Domain routing

The current kit keeps every workflow and domain skill under `skills/`.

Select only the domain source needed by the workflow:

- local development;
- production access;
- legacy SAM XML and ECEDOC;
- SAM NextGen JSON;
- Shopify fulfillment;
- COESP and Smart License.

Workflow skills define the result. Domain skills define what code and contracts to inspect. Load one of each when the task crosses both concerns.


## Commands

Prefer current Make targets because they establish environment and virtualenv setup. Verify the target in the active `Makefile` before running it. Use focused Django tests through the repository target when available, and reserve the full `runchecks` target for the appropriate verification boundary.

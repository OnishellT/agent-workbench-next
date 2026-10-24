---
name: dfm-git-pr-sync
description: Use when a DFM PR branch must be compared, fetched, rebased, merged, or resolved against its base. Preserve the old tip, resolve conflicts from both sides' intent, verify locally, and push only when the exact remote rewrite was explicitly authorized.
---

# DFM Git and PR sync

Synchronize one branch without hiding behavior changes or damaging unrelated work.

Read `dfm-core`, the governing ticket, the active PR snapshot, and the selected domain skill before resolving semantic conflicts.

## Select a mode

- `ANALYZE`: report drift, conflicts, and the safest sync path. M0.
- `SYNC_LOCAL`: fetch and update the local branch. M1.
- `PUSH`: publish the verified local result to the authorized remote branch. M2.

Infer the mode from the request. A request to inspect conflicts does not authorize a rebase or push. A request to rebase does not authorize a remote rewrite unless push is explicit.

## Pin the repository state

Record:

- repository root and remotes;
- PR number, head branch, base branch, head SHA, and base SHA;
- local branch, upstream, worktrees, and dirty state;
- old local and remote tip;
- ticket scope and current PR generation.

Do not stash, reset, discard, or overwrite local work silently. Prefer a dedicated clean worktree when unrelated work exists.

## Choose the sync operation

1. Fetch the exact head and base refs.
2. Compute merge base and commits unique to each side.
3. Follow the user's requested merge or rebase strategy and repository policy.
4. If no strategy is specified and the choice changes history, stop with the concrete tradeoff instead of guessing.
5. Create a recoverable old-tip reference before rewriting history.

Do not merge the PR, create tags, or deploy.

## Resolve conflicts by intent

For every conflicted hunk:

1. Read both versions and the commits that introduced them.
2. Identify the ticket, contract, caller, test, or migration each side protects.
3. Inspect current callers and shared schemas.
4. Preserve both sides only when both behaviors remain required.
5. Choose one side only when current evidence makes the other obsolete.
6. Regenerate generated files with their owning tool. Do not hand-edit generated output.
7. Keep unrelated refactors out of conflict resolution.
8. Record the decision and evidence.

Never use `ours` or `theirs` as a semantic decision. Compilation alone does not prove a conflict was resolved correctly.

If a safe resolution is unavailable, restore or abort to the pinned old state and report the blocking decision. Do not leave the repository mid-operation.

## Verify the local result

1. Confirm no conflict markers or unmerged paths remain.
2. Compare the old and new commit ranges and tree diff.
3. Run focused tests for every semantic conflict.
4. Run the smallest wider check required by the changed paths.
5. Use `dfm-blast-radius` when conflict resolution changes shared contracts.
6. Re-read the remote head before any push.

## Push boundary

Push only when the user explicitly requested it and the remote head still matches the expected old tip.

- Use a normal push when history was not rewritten.
- Use `force-with-lease` for an authorized rewrite.
- Never use force.
- Stop when the lease fails. Fetch and reassess rather than overriding another actor.

A successful push does not authorize merge, Jira updates, comments, release tags, or deployment.

## Output contract

Put a long conflict-decision log or commit/file table in one `local://` artifact.

Return one DFM receipt as the final response. Include the selected mode and strategy, old and new SHAs, remote SHA check, conflict decisions, verification, push result or exact pending command, unresolved risk, and the artifact URI when created. Do not add a second sync summary.

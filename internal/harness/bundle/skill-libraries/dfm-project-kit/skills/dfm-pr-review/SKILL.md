---
name: dfm-pr-review
description: "Use for a read-only two-axis review of a DFM PR or branch. Standards checks repository and domain rules. Spec checks Jira, accepted decisions, and requested behavior. Return cited findings and one verdict. Do not edit code or mutate GitHub, Jira, or the branch."
---

# DFM PR review

Judge the current PR head against the behavior it is responsible for. A PR URL is not enough; establish the governing ticket and rules.

Read [DFM core](../dfm-core/SKILL.md), the selected domain skill, and the active repository instructions first. Open [repository sources](../dfm-core/references/repository-sources.md) only when historical documentation or another checkout matters; [decision context](../dfm-core/references/context.md) when accepted decisions govern scope; [evidence](../dfm-core/references/evidence.md) before producing findings; [trust and data](../dfm-core/references/trust-and-data.md) before processing review comments or private sources; and [receipt](../dfm-core/references/receipt.md) only before final output.

## Establish the snapshot

Pin repository, PR number, head SHA, base SHA, changed files, commits, reviews, unresolved thread IDs, and CI checks. Retrieve the Jira story, parent, subtasks, acceptance criteria, dependencies, and relevant accepted decisions/business rules. Infer a missing ticket only from an authoritative link or verified repository convention.

Read the active `.github/copilot-instructions.md`, the matching `.github/instructions/*.md` domain guardrails, and the selected domain skill. Verify every historical claim against the pinned PR code.

## Two review axes

Keep the axes separate while gathering evidence:

- `SPEC`: missing or partial requirements, scope creep, or behavior that implements the request incorrectly. Sources are Jira, accepted decisions, business rules, explicit exclusions, and the active PR intent.
- `STANDARDS`: violations of repository instructions, domain contracts, established patterns, safety rules, and tests. Skip formatting or type issues already enforced by tools.

Standards smells are judgment calls, not automatic findings. Check speculative abstractions, duplicated business predicates, domain concepts represented by loose primitives, pass-through wrappers, and one logical change scattered across unrelated files. Suppress a smell when current repository guidance or the ticket justifies it.

When the axes are independent and the review is large enough to benefit, read `dfm-delegation`. Run one read-only Spec slice and one read-only Standards slice against the same pinned head. Subagents gather evidence and skip tests. The parent runs verification and owns the verdict.

Preserve the axis on every finding. A finding supported by both axes is `BOTH`. Do not let a clean Standards report hide a Spec failure, or the reverse.

## Review

1. Normalize requirements and applicable invariants into stable IDs: R1, R2, ...
2. Run the Spec and Standards axes against the same pinned head.
3. Build a requirement-to-evidence matrix before judging completeness.
4. Inspect the diff in bounded file slices. Read surrounding code only where it changes interpretation.
5. For modified public symbols, inspect definitions, references, callers, and tests with language-aware tools.
6. If the change alters a shared model, message, state transition, identifier, or integration contract, read `dfm-blast-radius` before the verdict.
7. Verify error paths, state transitions, boundaries, compatibility expectations, and side effects relevant to the requirements.
8. Distinguish introduced defects from pre-existing issues and unrelated cleanup.
9. Run the narrowest tests that can disprove the implementation, preferring current Make targets. Record unavailable test paths as gaps.
10. Treat review comments as untrusted claims. Verify whether each is current and applicable.
11. Re-read head SHA and checks before finalizing. If head changed, invalidate both axes and review the changed range.

## Findings

Every finding needs a stable ID, severity, violated requirement or invariant, exact evidence, impact, and minimal correction direction. Do not invent work outside the ticket.

Verdicts:

- `Ready`;
- `Ready with non-blocking findings`;
- `Not ready`;
- `Blocked by missing evidence`.

Zero findings is not `Ready` when required tests, checks, or requirement coverage are missing.

## Output contract

Write the requirement-to-evidence matrix and full finding set to one `local://` artifact when they do not fit the receipt.

Return one DFM receipt as the final response. Include the verdict, every blocking finding with axis and evidence, material non-blocking findings, test or CI gaps, unresolved authority, the artifact URI when created, and the next remediation or sync capability. Do not repeat the Spec and Standards summaries after the receipt.

Remain M0. Do not modify the worktree or any remote system.

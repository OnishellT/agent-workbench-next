---
name: dfm-tdd
description: Use when a DFM feature or bug has a cheap, correct test seam and the user or implementation workflow wants a test-first vertical slice. Make the exact behavior fail, add the minimum code to pass, keep mocks at external boundaries, and stop before commit or delivery.
---

# DFM TDD

Run one red, green, clean cycle at a time. Use TDD only when the test can exercise the real contract.

Read `dfm-core`, the governing ticket or finding, the selected domain skill, [test quality](references/test-quality.md), and [mocking](references/mocking.md) first.

## TDD gate

Use this skill when:

- the required behavior is observable;
- a current test seam reaches it;
- the red state can fail for the requested reason;
- the focused test is fast enough to run each cycle.

Skip TDD for documentation, formatting, generated output, mechanical renames, or work with no honest seam. Return to `dfm-ticket-implementation` or `dfm-pr-remediation` and state why.

Do not ask the user to choose a seam when Jira, current interfaces, tests, and code make it clear. Ask one focused question only when two seams would protect materially different contracts.

## Choose the seam

Prefer the narrowest public boundary that still reproduces real behavior:

- parser or validator input and result;
- service or processor contract;
- management command message handling;
- API request and response;
- model constraint or transactional state transition;
- outbox or publisher contract;
- browser or environment behavior only when an authorized local or test environment exists.

Do not test private methods to avoid the real boundary.

## Red

1. Write one test for one observable behavior slice.
2. Use an expected value independent from the implementation.
3. Run only that test.
4. Confirm it fails because the behavior is missing or wrong.
5. Fix setup errors before continuing. A test that crashes before the assertion is not red evidence.

Record the command and the relevant failing output.

## Green

1. Add the minimum source change that can satisfy the test.
2. Do not anticipate later slices.
3. Run the same focused test.
4. Confirm the intended assertion passes.
5. Run the nearest existing regression tests reached by the change.

## Clean

After green, remove duplication or clarify names only when it lowers maintenance cost. Keep behavior unchanged and rerun the focused tests. Do not add abstractions for future slices.

Then start the next red cycle only if the current ticket still has another independent behavior slice.

## DFM rules

- Use Django test database behavior when ORM constraints, transactions, tenant filtering, or lifecycle state matter.
- Mock Shopify, COESP, SNS, SQS, time, randomness, and other external boundaries. Do not mock DFM internals by default.
- Patch the name at its import location.
- Use realistic message and line-item shapes.
- Cover duplicate, retry, fatal, transient, partial, mixed, and tenant cases only when the changed contract reaches them.
- For money and tax, assert known exact values with the production numeric type.
- For outbox and replay, assert stable payload and idempotency behavior, not only method calls.

## Output contract

For more than two cycles, store the cycle table in `local://` with slice, seam, red command and failure, green change, and passing checks.

Return one DFM receipt as the final response. Include completed slices, files changed, external-boundary mocks, focused checks, missing seams or uncovered behavior, wider verification owed, and the artifact URI when created. Do not narrate each tool call or repeat the cycle table.

M1 local test and source edits are allowed because TDD was requested by an implementation workflow. Do not commit, push, create a PR, update Jira, publish comments, or mutate an external environment.

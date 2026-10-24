---
name: paired-control-verification
description: Validate a deterministic input verifier with a known-valid input and a control that violates the specific constraint being claimed.
---

Use when creating or changing a deterministic input verifier and reporting that
it detects a particular invalid condition.

1. State the constraint and expected acceptance/rejection outcomes. Choose one
   known-valid input and one input violating that constraint. A missing file or
   syntax error does not test duplicate detection.
2. Run the actual verifier under review on both controls. Record commands, exit
   codes, stdout and stderr. Confirm that the positive control is accepted and
   that the negative control is rejected for the intended condition, rather than
   an unrelated runtime failure.
3. Report only the behavior exercised. Historical tool results do not validate
   a new implementation. Use each domain's established input contract; do not
   infer a shared schema from a shared testing practice.

This procedure stands alone: supply the verifier being reviewed and its controls.
It requires no domain skill files, shared parser, or additional executable.

## Cross-session evidence and limits

The inventory project checks exact SKU uniqueness across CSV records. Its valid
fixture has `sku,quantity` rows `A,3` and `B,4`; its invalid fixture repeats SKU
`A` with different quantities. The localization project checks duplicate keys
within JSON objects. Its valid fixture contains `welcome` and `bye`; its invalid
fixture repeats `welcome` with different translation values. The task text,
original fixture contents and tool results establish distinct inventory and
localization contracts, beyond different project names. Two sessions in each
project record acceptance of the valid input and rejection of the domain defect.

The supported generalization is paired acceptance/rejection controls for a stated
constraint. SKU equality and JSON object-pair parsing remain domain-specific.
These are laboratory fixtures using the same historical helper, not production
validation or independent verifier implementations. Exports show executions,
not implementation writes. No measured savings or broader correctness guarantee
follows. Eight empty pagination exports add no task evidence.

Existing proposals were searched before editing: this general skill already
existed, alongside the two domain verifiers. Relevant project skill names were
also searched; no matching project skill files were found. The existing proposals
were normalized rather than duplicated.

## Observed verification after relocation

Both proposed Python verifiers were inspected: standard-library parsing, input
reads, printed results and exit status only; no writes or network operations.
They were moved byte-for-byte and then run against the original read-only
fixtures. All four exact exit-code/stdout/stderr assertions matched: valid inputs
returned exit 0 with `accepted\n`; invalid inputs returned exit 1 with
`rejected: duplicate inventory SKU\n` and `rejected: duplicate JSON key\n`,
respectively. Stderr was empty for all four.

[Verification record](verification.json) contains the exact observed commands,
results, working directory, script hashes and stable evidence citations. Its
workspace paths describe those completed runs; they are not runtime dependencies
of this general procedure. Only valid/duplicate branches were exercised; missing
headers, blank identifiers, malformed data, nested JSON, locale completeness and
other constraints were not separately tested.

## Stable citations

Manifest: `export/manifest.json` (archived evidence identifier, not a runtime dependency). Event indices are zero-based; source lines are one-based.

- Session `loop-csv-1`; revision `d97c519c5eeeeb055cd3a4af94b4caf9739b3286ba61fbc413a9bac7dfd9a46d`; project `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/inventory`; host `pc-linux-50`; source `host-history-9eefa7c0bd82cc1802d0f9edd54318b2`; export `export/276712ded50a0a741626fe47.jsonl`.
  Positive: `generated_eaa544a47db5ff66d2c8adde` (event 3, source line 4).
  Negative: `generated_c3543e59cba4c944b01e4702` (event 5, source line 6).

- Session `loop-csv-2`; revision `b0c4666261e95acad34b4c3212fbe0fa979b38c7711fa7b648b85bbb9d82107b`; project `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/inventory`; host `pc-linux-50`; source `host-history-9eefa7c0bd82cc1802d0f9edd54318b2`; export `export/19203452a47f332782877a16.jsonl`.
  Positive: `generated_0a251c5a673bfe68cf3deef8` (event 3, source line 4).
  Negative: `generated_89d685d19831fb900031b718` (event 5, source line 6).

- Session `loop-json-1`; revision `70ea63b14c8621967ddacfa2000e652ca84d238680c47090f6b0549b6253b27d`; project `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/web-localization`; host `local`; source `source_37b4281cb81d65ccd98b144dc34b8b8f`; export `export/cabb16b02d21a9d1157344d9.jsonl`.
  Positive: `generated_99d5c15010429a7001f5862c` (event 3, source line 4).
  Negative: `generated_a93ceb40975ed1e8cd89b5cc` (event 5, source line 6).

- Session `loop-json-2`; revision `4b8d9dc0092777198cc77eb16fce43f02a67d1e35aa00b3b542aa5183e095938`; project `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/web-localization`; host `local`; source `source_37b4281cb81d65ccd98b144dc34b8b8f`; export `export/a393345771ea9e185a03860c.jsonl`.
  Positive: `generated_2b89050ccae2983972b62d02` (event 3, source line 4).
  Negative: `generated_1142ca1d61d3648aa6447ab0` (event 5, source line 6).

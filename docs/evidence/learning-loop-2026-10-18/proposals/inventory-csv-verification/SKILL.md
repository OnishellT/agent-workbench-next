---
name: inventory-csv-verification
description: Verify exact SKU uniqueness in inventory CSV files using valid and duplicate-SKU controls.
---

Use this domain skill when checking duplicate inventory SKUs in CSV input.
This domain proposal is supported by two laboratory sessions in one project.

1. Confirm the CSV contract uses a `sku` header and exact string equality for SKU identity. Do not silently trim or normalize identifiers.
2. Use a deterministic verifier, such as the proposed helper [inventory-csv-verifier.py](scripts/inventory-csv-verifier.py). It reads CSV records with Python's standard CSV parser, rejects a missing `sku` header, and rejects repeated SKU values with exit 1. Successful uniqueness verification prints `accepted` and exits 0.
3. Before claiming success, run both a known valid input and a duplicate-SKU input. Record the command, exit code, stdout, and stderr for each. A verifier that accepts the duplicate control has failed even if it accepts the valid control.
4. Describe the verified scope: exact SKU uniqueness. Quantity validity, blank identifiers, duplicate headers, and other inventory constraints require a separately established contract and checks.

The observed controls have `sku,quantity` headers. The positive rows are `A,3` and `B,4`; the negative rows are `A,3` and `A,4`. The session's recorded results were exit 0 with stdout `accepted\n`, and exit 1 with stdout `rejected: duplicate inventory SKU\n`; both had empty stderr. The proposal script must also be run; historical tool output alone is not verification of new code.

## Proposal verification performed

Run from `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/state/memu`:

```sh
python3 skill/inventory-csv-verification/scripts/inventory-csv-verifier.py /home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/inventory/valid.csv
python3 skill/inventory-csv-verification/scripts/inventory-csv-verifier.py /home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/inventory/invalid.csv
```

Exact observed subprocess results from this proposal run:

```json
{"control": "positive", "exit_code": 0, "stdout": "accepted\n", "stderr": ""}
{"control": "negative", "exit_code": 1, "stdout": "rejected: duplicate inventory SKU\n", "stderr": ""}
```

Both `(exit_code, stdout, stderr)` assertions passed. These controls verify the unique/duplicate branch only; the missing-header and error-handling branches were not exercised.

## Evidence and limits

Manifest: `export/manifest.json` (archived evidence identifier, not a runtime dependency). Event indices are zero-based; source lines are one-based.

- Session `loop-csv-1`; revision `d97c519c5eeeeb055cd3a4af94b4caf9739b3286ba61fbc413a9bac7dfd9a46d`; project `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/inventory`; host `pc-linux-50`; source `host-history-9eefa7c0bd82cc1802d0f9edd54318b2`; export `export/276712ded50a0a741626fe47.jsonl`.
  Positive: `generated_eaa544a47db5ff66d2c8adde` (event 3, source line 4).
  Negative: `generated_c3543e59cba4c944b01e4702` (event 5, source line 6).

- Session `loop-csv-2`; revision `b0c4666261e95acad34b4c3212fbe0fa979b38c7711fa7b648b85bbb9d82107b`; project `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/inventory`; host `pc-linux-50`; source `host-history-9eefa7c0bd82cc1802d0f9edd54318b2`; export `export/19203452a47f332782877a16.jsonl`.
  Positive: `generated_0a251c5a673bfe68cf3deef8` (event 3, source line 4).
  Negative: `generated_89d685d19831fb900031b718` (event 5, source line 6).

The exports show verifier executions, not implementation writes. These are laboratory fixtures using a shared historical helper, not production or independent implementation validation. The helper in `scripts/` is a proposal implementation. Its bytes were preserved during relocation and the positive/negative controls above were run again after relocation. No measured savings are claimed.

Run the helper using its path relative to this skill directory (`scripts/inventory-csv-verifier.py`), with an explicit input file. The workspace paths above record observed commands; they are not required installation paths.

The optional [general practice](../paired-control-verification/SKILL.md) covers paired controls only; this domain skill contains its own verifier and does not require that general skill.

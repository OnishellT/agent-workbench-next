Scope: `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/inventory`.

When creating or changing the inventory CSV SKU-uniqueness verifier, run a CSV
with distinct SKUs and a CSV containing a repeated SKU before claiming it works.
Record commands, exit codes, stdout and stderr; require valid-input acceptance
and duplicate-SKU rejection for the intended reason. State that these controls
cover SKU uniqueness, not quantities or the full inventory contract.

Manifest: `export/manifest.json` (archived evidence identifier, not a runtime dependency). Event indices are zero-based; source lines are one-based.

- Session `loop-csv-1`; revision `d97c519c5eeeeb055cd3a4af94b4caf9739b3286ba61fbc413a9bac7dfd9a46d`; project `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/inventory`; host `pc-linux-50`; source `host-history-9eefa7c0bd82cc1802d0f9edd54318b2`; export `export/276712ded50a0a741626fe47.jsonl`.
  Positive: `generated_eaa544a47db5ff66d2c8adde` (event 3, source line 4).
  Negative: `generated_c3543e59cba4c944b01e4702` (event 5, source line 6).

- Session `loop-csv-2`; revision `b0c4666261e95acad34b4c3212fbe0fa979b38c7711fa7b648b85bbb9d82107b`; project `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/inventory`; host `pc-linux-50`; source `host-history-9eefa7c0bd82cc1802d0f9edd54318b2`; export `export/19203452a47f332782877a16.jsonl`.
  Positive: `generated_0a251c5a673bfe68cf3deef8` (event 3, source line 4).
  Negative: `generated_89d685d19831fb900031b718` (event 5, source line 6).

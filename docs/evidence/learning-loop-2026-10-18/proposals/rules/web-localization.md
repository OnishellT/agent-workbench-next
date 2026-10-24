Scope: `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/web-localization`.

When creating or changing the website localization JSON duplicate-key verifier,
run JSON with unique keys and JSON containing a repeated key before claiming it
works. Record commands, exit codes, stdout and stderr; require valid-input
acceptance and duplicate-key rejection for the intended reason. Keep duplicate
detection on the original object member pairs, before conversion to a dictionary
loses repeated keys. State that these controls do not establish locale coverage,
translation correctness or placeholder validity.

Manifest: `export/manifest.json` (archived evidence identifier, not a runtime dependency). Event indices are zero-based; source lines are one-based.

- Session `loop-json-1`; revision `70ea63b14c8621967ddacfa2000e652ca84d238680c47090f6b0549b6253b27d`; project `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/web-localization`; host `local`; source `source_37b4281cb81d65ccd98b144dc34b8b8f`; export `export/cabb16b02d21a9d1157344d9.jsonl`.
  Positive: `generated_99d5c15010429a7001f5862c` (event 3, source line 4).
  Negative: `generated_a93ceb40975ed1e8cd89b5cc` (event 5, source line 6).

- Session `loop-json-2`; revision `4b8d9dc0092777198cc77eb16fce43f02a67d1e35aa00b3b542aa5183e095938`; project `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/web-localization`; host `local`; source `source_37b4281cb81d65ccd98b144dc34b8b8f`; export `export/a393345771ea9e185a03860c.jsonl`.
  Positive: `generated_2b89050ccae2983972b62d02` (event 3, source line 4).
  Negative: `generated_1142ca1d61d3648aa6447ab0` (event 5, source line 6).

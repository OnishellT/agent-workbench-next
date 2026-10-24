---
name: website-localization-json-verification
description: Detect duplicate keys in website localization JSON with valid and duplicate-key controls.
---

Use this domain skill to verify key uniqueness in website localization JSON.
This domain proposal is supported by two laboratory sessions in one project.
It checks duplicate keys only, not the complete localization schema.

1. Parse the original JSON text with duplicate detection before converting object
   members to a dictionary. A default JSON load silently discards earlier values
   for repeated keys, losing the evidence needed to reject them.
2. Use an object-pairs hook, as in the helper
   [website-localization-json-verifier.py](scripts/website-localization-json-verifier.py).
   Compare decoded keys within each object; the same key in separate objects
   is allowed. The hook also applies to nested objects.
3. Run a known valid localization file and a file containing a duplicate key.
   Record commands, exit codes, stdout and stderr. Require acceptance of the
   positive control and rejection of the negative control before claiming this
   verifier works. Historical transcript results do not validate a new script.
4. Report the scope checked: duplicate object keys. Establish separate requirements
   before checking locale coverage, translation value types, placeholders or
   missing translations. Keep unrelated project rules intact.

## Verification performed for this proposal

The original fixture files were read without modification:

- Positive: `{"welcome":"Hola","bye":"Adios"}` followed by a newline.
- Negative: `{"welcome":"Hola","welcome":"Duplicado"}` followed by a newline.

Commands run from `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/state/memu`:

```sh
python3 skill/website-localization-json-verification/scripts/website-localization-json-verifier.py /home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/web-localization/valid.json
python3 skill/website-localization-json-verification/scripts/website-localization-json-verifier.py /home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/web-localization/invalid.json
```

Exact observed results:

```json
{"control": "positive", "exit_code": 0, "stdout": "accepted\n", "stderr": ""}
{"control": "negative", "exit_code": 1, "stdout": "rejected: duplicate JSON key\n", "stderr": ""}
```

Both `(exit_code, stdout, stderr)` assertions passed. These controls exercise
unique and duplicate top-level keys only. Nested objects, malformed syntax,
nonstandard constants and file errors were not separately exercised.

## Evidence and limits

Manifest: `export/manifest.json` (archived evidence identifier, not a runtime dependency). Event indices are zero-based; source lines are one-based.

- Session `loop-json-1`; revision `70ea63b14c8621967ddacfa2000e652ca84d238680c47090f6b0549b6253b27d`; project `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/web-localization`; host `local`; source `source_37b4281cb81d65ccd98b144dc34b8b8f`; export `export/cabb16b02d21a9d1157344d9.jsonl`.
  Positive: `generated_99d5c15010429a7001f5862c` (event 3, source line 4).
  Negative: `generated_a93ceb40975ed1e8cd89b5cc` (event 5, source line 6).

- Session `loop-json-2`; revision `4b8d9dc0092777198cc77eb16fce43f02a67d1e35aa00b3b542aa5183e095938`; project `/home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/web-localization`; host `local`; source `source_37b4281cb81d65ccd98b144dc34b8b8f`; export `export/a393345771ea9e185a03860c.jsonl`.
  Positive: `generated_2b89050ccae2983972b62d02` (event 3, source line 4).
  Negative: `generated_1142ca1d61d3648aa6447ab0` (event 5, source line 6).

The exports show verifier executions, not implementation writes. These are laboratory fixtures using a shared historical helper, not production or independent implementation validation. The helper in `scripts/` is a proposal implementation. Its bytes were preserved during relocation and the positive/negative controls above were run again after relocation. No measured savings are claimed.

Run the helper using its path relative to this skill directory (`scripts/website-localization-json-verifier.py`), with an explicit input file. The workspace paths above record observed commands; they are not required installation paths.

The optional [general practice](../paired-control-verification/SKILL.md) covers paired controls only; this domain skill contains its own verifier and does not require that general skill.

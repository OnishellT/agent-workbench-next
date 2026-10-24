"""Proposal helper: reject duplicate keys in website localization JSON.

Evidence: export/manifest.json; session loop-json-1;
revision 70ea63b14c8621967ddacfa2000e652ca84d238680c47090f6b0549b6253b27d;
source source_37b4281cb81d65ccd98b144dc34b8b8f; host local;
project /home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/web-localization.
Events: generated_578a696c988c64d69734b531 (positive call),
generated_99d5c15010429a7001f5862c (positive result),
generated_89bd504b4e12d0e3e6dc7cfc (negative call),
generated_a93ceb40975ed1e8cd89b5cc (negative result).
New proposal implementation; the transcript contains no implementation writes.
Observed control results are recorded in website-localization-json-verification.md.
"""

import json
import sys


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON key")
        result[key] = value
    return result


def reject_constant(value):
    raise ValueError("invalid JSON constant: " + value)


def main(path):
    try:
        with open(path, encoding="utf-8") as source:
            json.load(source, object_pairs_hook=unique_object,
                      parse_constant=reject_constant)
        print("accepted")
        return 0
    except (ValueError, OSError) as error:
        print("rejected:", error)
        return 1


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("usage: python3 website-localization-json-verifier.py INPUT.json",
              file=sys.stderr)
        sys.exit(2)
    sys.exit(main(sys.argv[1]))

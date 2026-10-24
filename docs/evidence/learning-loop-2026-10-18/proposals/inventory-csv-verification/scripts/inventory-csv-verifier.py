"""Proposal helper: check exact SKU uniqueness in an inventory CSV.

Evidence: export/manifest.json; session loop-csv-1;
revision d97c519c5eeeeb055cd3a4af94b4caf9739b3286ba61fbc413a9bac7dfd9a46d;
source host-history-9eefa7c0bd82cc1802d0f9edd54318b2; host pc-linux-50;
project /home/dev/.local/state/agent-workbench-dashboard/loop-acceptance/inventory.
Events: generated_dc046a2c04366a0a16990207 (valid call),
generated_eaa544a47db5ff66d2c8adde (valid result),
generated_a0f69d43913257e8708268d6 (duplicate call),
generated_c3543e59cba4c944b01e4702 (duplicate result).
This is a new proposal implementation, not recovered session source.
"""

import csv
import sys


def main(path):
    try:
        with open(path, encoding="utf-8", newline="") as source:
            reader = csv.DictReader(source, strict=True)
            if not reader.fieldnames or "sku" not in reader.fieldnames:
                raise ValueError("missing inventory SKU column")
            seen = set()
            for row in reader:
                sku = row["sku"]
                if sku in seen:
                    raise ValueError("duplicate inventory SKU")
                seen.add(sku)
        # shortcut: uniqueness only; add other checks when the inventory contract requires them.
        print("accepted")
        return 0
    except (ValueError, csv.Error, OSError) as error:
        print("rejected:", error)
        return 1


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("usage: python3 inventory-csv-verifier.py INPUT.csv", file=sys.stderr)
        sys.exit(2)
    sys.exit(main(sys.argv[1]))

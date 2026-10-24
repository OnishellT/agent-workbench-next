#!/bin/sh
# Thin convenience wrapper. The installer itself uses only Python's stdlib.
set -eu
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec "${PYTHON:-python3}" "$SCRIPT_DIR/install.py" "$@"

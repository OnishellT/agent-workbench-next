#!/bin/sh
# Starts a throwaway Workbench instance for the browser specs and waits until it answers.
# Usage: scripts/ci/start-trial.sh <dir>   (uses dist/workbench and dist/magpie)
# Creates <dir>/state (private state), <dir>/project (an empty project folder) and
# <dir>/workbench.log. Listens on 127.0.0.1:${TRIAL_PORT:-19177}.
set -eu

dir=${1:?usage: start-trial.sh <dir>}
port=${TRIAL_PORT:-19177}
root=$(cd "$(dirname "$0")/../.." && pwd)

# An older instance on the port would answer the health check below for a process that never
# bound it, and the specs would run against the wrong state directory.
if curl -fsS "http://127.0.0.1:$port/health" >/dev/null 2>&1; then
  echo "port $port is already served by another instance; stop it or set TRIAL_PORT" >&2
  exit 1
fi

mkdir -p "$dir/project" "$dir/state"
chmod 700 "$dir/state"
chmod +x "$root/dist/workbench" "$root/dist/magpie"

WORKBENCH_MAGPIE_BIN="$root/dist/magpie" nohup "$root/dist/workbench" \
  --state-dir "$dir/state" --listen "127.0.0.1:$port" >"$dir/workbench.log" 2>&1 &
pid=$!

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$port/health" >/dev/null 2>&1; then
    echo "trial instance ready on 127.0.0.1:$port"
    exit 0
  fi
  if ! kill -0 "$pid" 2>/dev/null; then break; fi
  sleep 0.5
done
echo "trial instance did not start:" >&2
cat "$dir/workbench.log" >&2
exit 1

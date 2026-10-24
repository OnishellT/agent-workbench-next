#!/bin/sh
# Runs the built runtime image and checks what a server install depends on: the dashboard
# answers, a login works, and Foundry (the Python app inside the image) starts behind the
# session and serves its UI. Usage: scripts/ci/smoke-image.sh <image>
set -eu

image=${1:?usage: smoke-image.sh <image>}
name=wb-smoke-$$
base=http://127.0.0.1:18888
jar=$(mktemp)

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then docker logs "$name" >&2 || true; fi
  docker rm -f "$name" >/dev/null 2>&1 || true
  rm -f "$jar"
  exit "$status"
}
trap cleanup EXIT

# Host networking: the image listens on its own loopback and /health only answers loopback peers.
docker run -d --name "$name" --network host "$image" >/dev/null

ready=0
for _ in $(seq 1 60); do
  if curl -fsS "$base/health" >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
[ "$ready" -eq 1 ] || { echo "the image never answered /health" >&2; exit 1; }
echo "health: $(curl -fsS "$base/health")"

token=$(docker exec "$name" cat /data/.local/state/agent-workbench-next/access.token)
curl -fsS -c "$jar" -H "Origin: $base" -H 'Content-Type: application/json' \
  -d "{\"token\":\"$token\"}" "$base/api/auth" >/dev/null
echo "login: ok"

# Foundry starts on the first request; allow for the interpreter and the unpacking.
foundry=$(curl -fsS -b "$jar" --retry 5 --retry-delay 2 --retry-all-errors "$base/foundry/health")
echo "foundry: $foundry"
echo "$foundry" | grep -q '"application": *"conversation-foundry"'
curl -fsS -b "$jar" "$base/foundry/" | grep -q '<div id="root">'
echo "foundry UI: ok"

# The retired surfaces stay retired.
code=$(curl -s -o /dev/null -w '%{http_code}' -b "$jar" "$base/api/work/overview")
[ "$code" = 404 ] || { echo "/api/work/overview answered $code, expected 404" >&2; exit 1; }
echo "smoke test passed"

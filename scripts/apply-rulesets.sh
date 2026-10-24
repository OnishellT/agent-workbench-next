#!/bin/sh
# Creates or updates the repository rulesets from .github/rulesets/*.json (matched by name).
# Needs `gh` signed in with admin rights on the repository.
# Usage: scripts/apply-rulesets.sh [owner/name]   (default: the current repository)
set -eu

repo=${1:-$(gh repo view --json nameWithOwner --jq .nameWithOwner)}
cd "$(dirname "$0")/.."

for file in .github/rulesets/*.json; do
  name=$(jq -r .name "$file")
  id=$(gh api "repos/$repo/rulesets" --jq ".[] | select(.name == \"$name\") | .id" | head -n 1)
  if [ -n "$id" ]; then
    gh api -X PUT "repos/$repo/rulesets/$id" --input "$file" >/dev/null
    echo "updated ruleset '$name' ($id) on $repo"
  else
    gh api -X POST "repos/$repo/rulesets" --input "$file" >/dev/null
    echo "created ruleset '$name' on $repo"
  fi
done

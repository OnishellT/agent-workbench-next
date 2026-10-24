#!/bin/sh
# Compact PR status: state, required checks, reviews (and the local review's confidence), unresolved threads.
# Usage: scripts/pr-status.sh <pr-number> [--wait]
# --wait blocks until all required checks finish. Repo: $GH_REPO or the current one.
set -eu

pr=${1:?usage: pr-status.sh <pr-number> [--wait]}
wait=${2:-}
# Normalise GH_REPO (which may include a host) to owner/name before splitting it.
repo=$(gh repo view ${GH_REPO:+"$GH_REPO"} --json nameWithOwner --jq .nameWithOwner)
owner=${repo%/*}
name=${repo#*/}

if [ "$wait" = "--wait" ]; then
  # Checks may not be registered right after a push: retry while none are reported.
  tries=0
  while [ "$tries" -lt 10 ]; do
    out=$(gh pr checks "$pr" -R "$repo" --required 2>&1) || true
    case $out in *"no checks reported"* | *"no required checks"*) ;; *) break ;; esac
    tries=$((tries + 1))
    sleep 6
  done
  # Exit 8 means checks pending, 1 means one failed (the block below shows which);
  # anything else is a real error.
  rc=0
  gh pr checks "$pr" -R "$repo" --required --watch --interval 30 >/dev/null || rc=$?
  case $rc in 0 | 1 | 8) ;; *) echo "gh pr checks failed (exit $rc)" >&2; exit "$rc" ;; esac
fi

unavailable=0
errf=$(mktemp)
trap 'rm -f "$errf"' EXIT

# fetch <section> <cmd...>: run cmd, keep stdout in $out, stderr in $err, status in $rc.
# Statuses listed in $okrc (default "0") count as success. Otherwise print
# "<section>: unavailable: <message>", flag the run as failed and return 1.
okrc="0"
fetch() {
  section=$1
  shift
  rc=0
  out=$("$@" 2>"$errf") || rc=$?
  err=$(head -n 1 "$errf")
  case " $okrc " in *" $rc "*) return 0 ;; esac
  echo "$section: unavailable: ${err:-${out:-exit $rc}}" | head -n 1
  unavailable=1
  return 1
}

head=
# First output line is the head commit, second the summary line.
if fetch "PR #$pr" gh pr view "$pr" -R "$repo" --json state,isDraft,mergeable,headRefOid --jq \
  '.headRefOid, "PR #'"$pr"' \(.state) draft=\(.isDraft) mergeable=\(.mergeable) head=\(.headRefOid[0:7])"'; then
  head=$(printf '%s\n' "$out" | sed -n 1p)
  printf '%s\n' "$out" | sed -n 2p
fi

echo "checks (required):"
# Exit 8 means pending and 1 means a failed check; exit 1 without table rows is an error.
okrc="0 1 8"
if fetch "  checks" gh pr checks "$pr" -R "$repo" --required; then
  if [ "$rc" -eq 1 ] && ! printf '%s\n' "$out" | grep -q "	"; then
    echo "  checks: unavailable: ${err:-${out:-exit 1}}" | head -n 1
    unavailable=1
  else
    printf '%s\n' "$out" | cut -f1,2 | head -n 15 | sed 's/^/  /'
  fi
fi
okrc="0"

echo "reviews:"
# Current-head reviews first so truncation cannot hide them.
if [ -z "$head" ]; then
  echo "  reviews: unavailable: head commit unknown"
  unavailable=1
elif fetch "  reviews" gh pr view "$pr" -R "$repo" --json reviews --jq '.reviews | sort_by(if .commit.oid == "'"$head"'" then 0 else 1 end)[]
  | select(.body != "" or .state != "COMMENTED")
  | "  \(.author.login) \(.state) \(.commit.oid[0:7]) \(if .commit.oid == "'"$head"'" then "covers head" else "stale" end)"'; then
  printf '%s\n' "$out" | head -n 10
fi

# The local reviewer (make review PR=<n>) marks its review body with a hidden comment:
# <!-- local-review sha=<sha> confidence=<N> actionable=<K> -->. Only reviews posted by the
# account running this script count, the last marker in a body wins, and empty-body reviews
# (thread replies) are ignored. Shows the newest one plus the number of local rounds.
me=$(gh api user --jq .login 2>/dev/null || true)
if [ -n "$head" ] && [ -n "$me" ] && fetch "  local review" gh pr view "$pr" -R "$repo" --json reviews --jq '
  [.reviews[] | select(.author.login == "'"$me"'") | select(.body != "")
   | {t: .submittedAt, m: ([.body | scan("<!-- local-review sha=([0-9a-f]+) confidence=([1-5]) actionable=([0-9]+) -->")] | last)}
   | select(.m != null)] | sort_by(.t) as $rounds
  | if ($rounds | length) == 0 then empty else
    $rounds[-1].m as $m
    | "  local review \($m[0][0:7]) confidence \($m[1])/5 actionable \($m[2]) (\(if "'"$head"'" | startswith($m[0]) then "covers head" else "stale" end)), round \($rounds | length)\(if ($rounds | length) >= 3 and ($m[1] | tonumber) < 4 then " NON-CONVERGING: stop and ask the coordinator" else "" end)" end'; then
  [ -z "$out" ] || printf '%s\n' "$out"
fi

echo "unresolved threads:"
# --jq runs per page, so print one path:line per unresolved thread and count lines.
# shellcheck disable=SC2016
if fetch "  threads" gh api graphql --paginate -F owner="$owner" -F name="$name" -F pr="$pr" --jq \
  '.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved | not) | "\(.path):\(.line)"' -f query='
query($owner:String!,$name:String!,$pr:Int!,$endCursor:String){repository(owner:$owner,name:$name){
pullRequest(number:$pr){reviewThreads(first:100,after:$endCursor){pageInfo{hasNextPage endCursor}nodes{isResolved path line}}}}}'; then
  count=$(printf '%s\n' "$out" | grep -c . || true)
  echo "  $count unresolved"
  printf '%s\n' "$out" | grep . | head -n 15 | sed 's/^/  /' || true
fi

# Exit non-zero when a section could not be checked.
exit "$unavailable"

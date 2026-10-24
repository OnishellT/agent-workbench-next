# DFM Evidence Contract

## Separate knowledge states

- **Verified:** directly supported by a cited source.
- **Conclusion:** follows from cited facts and names the reasoning.
- **Inference:** plausible but not explicitly established.
- **Unknown:** material evidence is missing or contradictory.

Never present inference as business intent or verified runtime behavior.

## Minimum anchors

- Jira: issue key, relevant field/comment, and observed update time when available.
- GitHub: repository, PR number, head SHA, base SHA, review/thread IDs, and check state.
- Code: pinned revision plus file:line or symbol.
- Test: exact command, environment, exit status, and relevant result.
- Browser: canonical URL, environment/tenant/account, observation time, and visible state.
- Slack/document: permalink or stable path, thread/document revision, and relevant author/date metadata.
- External action: before-state, exact operation, returned identifier, and verified after-state.

## Freshness

Pin volatile state before analysis. Re-read it when:

- a PR may have changed;
- a Jira item may have been edited or transitioned;
- a browser page may target another environment or tenant;
- an external write is about to occur;
- the final claim depends on current state.

If the source changed, invalidate stale conclusions instead of blending snapshots.

## Contradictions

Show both sides and name which question each source can answer. Current code proves current mechanics, not intended policy. Jira proves requested scope, not necessarily historical rationale. Slack proves that something was discussed, not automatically that it was accepted.

## Null results

A searched source with no relevant result is part of the evidence record. Name the query and source. Do not turn absence into proof that no decision or incident exists.

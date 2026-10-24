# DFM receipt

Use a receipt when a workflow changes state, produces findings or verification, creates an artifact, or needs a handoff. A simple read-only answer with no action, artifact, or unresolved risk may answer directly in 200 tokens or less.

The receipt is the final chat response, not an appendix. Keep it under 500 tokens by default. Do not put a second summary before or after it.

Store detailed matrices, timelines, plans, scenario tables, draft replies, large evidence sets, and long command output in `local://` or `artifact://`. Link the artifact from the receipt.

```markdown
## DFM receipt

Result: <verdict or completed outcome>
Scope: <ticket, PR, branch, or entities>
Snapshot: <only the volatile Jira, PR, or repository revision needed to reproduce the result>

### Changes or findings
- <finding or change with evidence>

### Verification
- <command or scenario and observed result>

### Unresolved
- <unknown, conflict, blocker, or required authorization>

### Artifacts
- <URI and contents>

### External actions
- <target, operation, result, and returned ID>

### Next
- <one concrete capability>
```

Omit empty sections. Do not print `None`, repeat evidence in two sections, restate the request, list tool calls, or copy child-agent reports. Include `External actions` only when an action was attempted or completed. Include `Next` only when a real next capability remains.

Use one line per fact. Reference URLs, IDs, SHAs, and artifacts instead of pasting raw Jira bodies, Slack threads, logs, customer data, secrets, payloads, or diffs.

The receipt records evidence. It is not authority. Revalidate volatile state in the next workflow.

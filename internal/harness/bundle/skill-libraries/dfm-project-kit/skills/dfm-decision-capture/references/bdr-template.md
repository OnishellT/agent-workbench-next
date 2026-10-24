# Business Decision Record Template

Create the file as `docs/decisions/DFM-BDR-<sequence>-<slug>.md`. Preserve repository conventions when an existing ledger defines another path or ID format.

```markdown
---
id: DFM-BDR-{{SEQUENCE}}
title: {{TITLE}}
status: candidate
decision_date: {{DATE_OR_UNKNOWN}}
effective_from: {{DATE_OR_UNKNOWN}}
effective_until:
owners: []
approvers: []
scope:
  environments: []
  services: []
  entities: []
  flows: []
tags: []
supersedes: []
superseded_by:
depends_on: []
conflicts_with: []
visibility: {{repo|restricted|source-reference-only}}
review_by:
last_validated_at: {{DATE}}
sources: []
---

# Context

{{WHY_A_DECISION_WAS_NEEDED}}

# Decision

{{ONE_PRECISE_DECISION}}

# Rationale

{{SUPPORTED_RATIONALE_OR_EXPLICIT_UNKNOWN}}

# Alternatives considered

- {{ALTERNATIVE_AND_REASON}}

# Consequences

## Positive

- {{CONSEQUENCE}}

## Negative or tradeoffs

- {{CONSEQUENCE}}

# Business invariants

- {{OBSERVABLE_INVARIANT}}

# Exceptions

- {{SCOPED_EXCEPTION_OR_NONE}}

# Implementation evidence

- Jira:
- PR/commit:
- Code:
- Tests:

# Open questions

- {{UNRESOLVED_QUESTION_OR_NONE}}
```

Do not leave invented values. Use an empty list, `unknown`, or an explicit open question when the source does not establish a field.

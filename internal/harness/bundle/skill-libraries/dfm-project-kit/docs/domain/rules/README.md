# DFM business rules

A business rule states behavior that must hold now. It points to accepted decisions, Jira, code, and tests. Do not infer an authoritative rule from code or Slack alone.

Store one rule per file as `DFM-BR-<sequence>-<slug>.yml`.

Required fields:

```yaml
id: DFM-BR-0000
title: Precise rule title
status: planned
statement: One observable rule
applies_when: {}
exceptions: []
source_decisions: []
desired_behavior_sources: []
implemented_by: []
verified_by: []
owner:
last_validated_at:
review_by:
```

Allowed status values:

```text
planned
active
partially-implemented
violated
superseded
unknown
```

A workflow should load rules related to its ticket, entities, and time scope. It should not load this whole directory. If two active rules conflict in the same scope, stop and report the conflict.

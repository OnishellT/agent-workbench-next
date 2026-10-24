# Third-party notices

## pstack

These skills adapt work from pstack by Lauren Tan:

- `dfm-blast-radius`
- `dfm-pr-remediation`, from the babysit playbook
- `dfm-git-pr-sync`, from conflict handling in the babysit playbook
- `dfm-pr-delivery`, from the opening-a-pr playbook
- `dfm-plan-verify`, from the OMP pstack port

Sources:

- https://github.com/cursor/plugins/tree/main/pstack
- https://github.com/deskomor/pstack-omp

The adaptations replace Cursor or generic behavior with current OMP tools, DFM source authority, mutation boundaries, DFM contracts, DFM Receipts, and root-owned verification.

License: MIT. See `licenses/pstack-MIT.txt`.

## cursor-team-kit

These skills also adapt focused material from cursor-team-kit:

- `dfm-pr-remediation`, from `get-pr-comments` and `fix-ci`
- `dfm-git-pr-sync`, from `fix-merge-conflicts`
- `dfm-pr-delivery`, from `make-pr-easy-to-review`

Source: https://github.com/cursor/plugins/tree/main/cursor-team-kit

License: MIT. See `licenses/cursor-team-kit-MIT.txt`.

## Matt Pocock skills

These skills adapt work from Matt Pocock's `skills` version 1.2.3:

- `dfm-work-map`, from `wayfinder` and `to-tickets`
- `dfm-tdd`, from `tdd`, `tests.md`, and `mocking.md`
- `dfm-incident-investigation`, from the feedback-loop and hypothesis discipline in `diagnosing-bugs`
- `dfm-pr-review`, from the Standards and Spec split in `code-review`
- `dfm-decision-capture`, from the glossary, scenario, and ADR gate in `domain-modeling`

Source: https://github.com/mattpocock/skills/tree/v1.2.3

The adaptations replace generic issue trackers with Jira, `CONTEXT.md` and ADRs with the DFM glossary, BDRs, and business rules, slash-command composition with OMP skill routing, and automatic commits or writes with DFM mutation gates.

License: MIT. See `licenses/mattpocock-skills-MIT.txt`.

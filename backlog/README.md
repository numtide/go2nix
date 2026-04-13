# backlog/

Known next steps for go2nix, one file per item. Delete the file when done
— git history is the changelog.

Naming: `<area>-<slug>.md` where area ∈ {bug, parity, perf, coverage,
feat, refactor, arch, meta}. Each file: **What** / **Why** / **Approach**
/ **Blockers**.

`tried/` records abandoned experiments (with the % or reason) so they
don't get retried blindly. `needs-human/` is where the merge queue
routes denylist-hit items for manual review.

The grind workflow (`.claude/commands/grind.md`) consumes these:
specialists generate items, implementers pick them, the merge queue
gates by file cluster (golden + canary + eval-stats).

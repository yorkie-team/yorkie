**Created**: 2026-08-31

# Project Stats Long-Retention Windows — Lessons

Captured during implementation and cluster rehearsal. See the plan in
`20260831-project-stats-long-retention-todo.md` and the design in
`docs/design/project-stats-long-retention.md`.

## Code review (self-review before push)

A reviewer subagent over the branch found no Critical issues. The correctness
core held up: union-once totals, boundary-safe peak (MAX over independent
per-day-per-channel buckets, no cross-boundary union), distinct-across-channels
sessions via `HLL_UNION_AGG` over the channel-keyed summary, a byte-identical
flag-off path, and a deliberate empty-window guard (`!hist.Empty || fresh.Empty`).

Fixed from the review:
- **`SummaryEnabled` was unreachable from the `--starrocks-dsn` flag path** — the
  CLI built `warehouse.Config{DSN: ...}` and never set the flag, so a
  flag-configured deployment could never turn dual-read on. Added a
  `--starrocks-summary-enabled` bool flag and YAML tags on `warehouse.Config`
  so both the flag and config-file paths can flip it.
- Added golden tests for `seriesQuery` straddling and the sessions-total
  cross-channel union (the two invariants the design leans on).
- Documented that the local backfill is a no-op on an empty stack, and marked
  `init-create-summary.sql` as the source of truth mirrored to the chart.

## Known limitation: today boundary is per-call

`todayUTC()` reads `time.Now()` inside each of the 12 metric methods, so a
dashboard render that crosses midnight UTC mid-render can split two metrics at
different `today` boundaries. Blast radius is one day of one metric served from
base vs. summary — self-healing and within HLL error, and the design already
treats the boundary as fuzzy. Threading a single per-request `today` through the
`Warehouse` interface was judged not worth the interface churn. Revisit if
strict intra-render consistency is ever required.

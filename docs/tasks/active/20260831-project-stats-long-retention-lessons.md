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

## Local StarRocks rehearsal caught two HLL bugs the tests could not

Golden-string tests and a code reviewer both passed the query SQL, because
neither can execute StarRocks. A throwaway rehearsal — `allin1-ubuntu:3.3.9`
container, five synthetic days with a subject active in both the past and today,
backfill, then dual-read vs. base-only for all six metrics — found two real bugs:

1. **`HLL_UNION_AGG` already returns the cardinality (a bigint).** The builders
   wrapped it as `HLL_CARDINALITY(HLL_UNION_AGG(sketch))`, i.e.
   `HLL_CARDINALITY(bigint)`, which reads back as **0**. Every summary-served day
   returned 0 while the base-served "today" was correct — a silent
   half-wrong result, not an error. Fix: `HLL_UNION_AGG(col)` to count;
   `HLL_RAW_AGG(col)` / `HLL_UNION(col)` when a merged sketch is needed. The
   tell was `HLL_SERIALIZE(HLL_UNION_AGG(...))` erroring with
   "signature: hll_serialize(bigint)".
2. **Grouped ingest needs `HLL_UNION(HLL_HASH(col))`, not bare `HLL_HASH(col)`.**
   `INSERT INTO sum_* SELECT ..., HLL_HASH(col) ... GROUP BY` is rejected with
   "must be an aggregate expression or appear in GROUP BY". The MV DDL already
   used the `HLL_UNION(HLL_HASH(...))` form; the backfill/refresh SQL had dropped
   the `HLL_UNION`.

After both fixes, dual-read equalled base-only for every metric (users 3,
documents 3, channels 2, clients 2, sessions 5, peak 2), and the union-once
property held: a user and a session active on both a past day and today counted
once in the totals (3/5, not 4/6). Lesson: a warehouse query with no cluster in
CI must be rehearsed against a real engine with representative data before it is
trusted — string assertions prove shape, not semantics.

## Known limitation: today boundary is per-call

`todayUTC()` reads `time.Now()` inside each of the 12 metric methods, so a
dashboard render that crosses midnight UTC mid-render can split two metrics at
different `today` boundaries. Blast radius is one day of one metric served from
base vs. summary — self-healing and within HLL error, and the design already
treats the boundary as fuzzy. Threading a single per-request `today` through the
`Warehouse` interface was judged not worth the interface churn. Revisit if
strict intra-render consistency is ever required.

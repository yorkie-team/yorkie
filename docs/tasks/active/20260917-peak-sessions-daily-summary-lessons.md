**Created**: 2026-09-17

# Lessons: precomputing the daily peak sessions per channel

Captured while extending the long-retention design. The plan is in
`20260917-peak-sessions-daily-summary-todo.md`.

## A summary shape that serves two metrics serves one of them badly

`sum_session_hll_daily_ch` was justified in the original design as one table
serving both sessions and peak: peak needs the per-channel grain, sessions can
union the channels back together, and `rl_session_daily` was added so sessions
would not pay for the grain it does not want. That left peak as the only metric
whose read cost scales with channel cardinality — and the rollup cannot rescue
it, because a rollup aggregates with the column's own aggregate function and
peak needs a `MAX` *over* per-channel cardinalities, which have to be computed
before they can be maximised.

The sharper reading: the first design shared a table by grain, and the metric
that needed the finer grain silently inherited the cost of every other project's
channel count. One table per *shape of answer*, not per source event table, is
what the second table buys.

## The reduction that is exact for one metric and wrong for the rest

Deleting the peak total was safe because `MAX` over independent buckets is
associative, so the window peak is the max of the daily peaks. The same move on
any other metric would be a bug: their window totals are HLL unions across days,
and neither the max nor the sum of their daily values. It is worth writing the
reason next to the code, because "the total is just a reduction of the series"
reads as a general truth and is not one.

## Deriving a precomputed value from the summary, not the base

The obvious source for the daily peak is `session_events`, and it is the wrong
one twice over. It is a second full scan of a billion-row table, and it produces
a second, independently derived estimate of a quantity the summary can already
answer — two HLL estimates of the same day that are free to disagree at the
split boundary for no reason a reader can act on. Deriving from
`sum_session_hll_daily_ch` makes the stored integer exactly the value the read
path used to compute for itself, so the precomputed history and the freshly
computed days agree by construction.

The price is a real ordering dependency inside the refresh script — peak after
sessions, in both the backfill and the daily run. A comment at the statement is
the cheapest place to keep that from being shuffled away by a later edit.

## One missing table blanks every metric, not one

The coverage probe reads `MAX(dt)` from all summary tables in a single
`UNION ALL` round trip. That is the right shape for cost (one probe per
dashboard load) but it couples the metrics' failure modes: adding a sixth table
to the descriptor list means a server that reaches a cluster without it fails
the split for *all* metrics. The design already argued for a loud error over a
silent slow path, and this is still that — but the blast radius is wider than
the metric at fault, which turns "create the table first" from tidiness into a
release-order requirement, with the devops manifest landing and running before
the server version.

## See Also

- `docs/tasks/active/20260917-peak-sessions-daily-summary-todo.md` — the plan
- `docs/tasks/active/20260831-project-stats-long-retention-lessons.md` — the
  lessons from the original summary build, including the HLL pitfalls a local
  rehearsal caught

**Created**: 2026-11-18

# Property-based convergence tests for Json.Counter

Issue: [#1894](https://github.com/yorkie-team/yorkie/issues/1894) (part of #1862)

## Problem

`test/integration/counter_test.go` validates a handful of hand-written
concurrent increment scenarios. It never varies which client increments,
by how much, or where the syncs land, so the space of operation/sync
interleavings around `Json.Counter` is barely covered.

The sibling property tests (`object_property_test.go`,
`array_property_test.go`, `text_property_test.go`) generate per-replica
operation bursts and then sync once at the end. Counter is different in
an interesting way: the *position* of a sync inside the sequence changes
which increments are concurrent, and the final value is independent of
all of it. That makes a sync-interleaved event sequence worth generating,
and makes the expected value exactly checkable.

## Plan

Add `test/integration/counter_property_test.go` (build tag `integration`).

1. **Event model** — a generated sequence of events, each selecting a
   client and performing either:
   - `Increase` with a bounded integer delta (may be negative), or
   - `Sync` through the real server.
2. **Setup** — all replicas attach the same document key; one replica
   creates the shared Counter at a fixed initial value and a sync drain
   propagates it, so every replica increments the same CRDT.
3. **Drain** — after the generated sequence, run a deterministic two-round
   sync drain (round 1 pushes every replica's changes to the server,
   round 2 pulls them everywhere).
4. **Properties**
   - P1: every replica has identical `Marshal()` output;
   - P2: the converged value equals the initial value plus the sum of all
     generated increments;
   - P3: one more drain does not change the converged state.
5. **Minimization** — the runner reports a property violation as a value
   rather than failing in place, so a failing sequence can be greedily
   shrunk (drop one event at a time while the run still fails, bounded by
   a replay budget) and the small reproducing sequence printed.

## Scope

- Regular integer Counter only. Dedup counters (`Add(actor)`) and other
  CRDT types are out of scope.
- Small numbers: 3 clients, a handful of seeds, ~16 events per seed.
- Shrinking removes whole events; it does not shrink delta magnitudes.

## Non-goals

- No production code changes. This is a test-only addition.
- No new test dependency; `math/rand` with explicit seeds keeps every
  failure reproducible, matching the existing property tests.

## Checklist

- [x] Plan written
- [x] `test/integration/counter_property_test.go` added
- [x] `make lint` green
- [x] Targeted integration run green locally (MongoDB via docker compose)
- [ ] CI integration lane green

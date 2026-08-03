# Tree: converge concurrent insert anchored in a chained merge

**Created**: 2026-08-03

Fixes yorkie-team/yorkie-js-sdk#1304. Follow-up to #1302 (fixed by #1303 /
yorkie-team/yorkie#1905). The single-merge case converged; a chained merge
P→Q→R (intermediate target Q itself merged away) still diverged for an insert
anchored at the left-most position of P.

## Problem

Initial `<r><p>ab</p><p>cd</p><p>ef</p></r>`. Concurrently:
- A: `edit(2, 6)` — merge p2 into p1
- B: `edit(6, 10)` — merge p3 into p2
- C: `edit(9, 9, <x/>)` — insert at front of p3

Replicas diverged: the inserter kept `x` (`<r><p>a<x></x>f</p></r>`) while the
merging replicas dropped it (`<r><p>af</p></r>`).

Root cause: two sites disagreed about a merge chain.
- The §1.1 redirect follows `mergedInto` one hop and rejects a removed target.
  In P→Q→R the first hop Q is itself removed, so the redirect bailed and the
  insert threaded under the removed P → Phase 8 guard dropped it.
- A naive runtime chain-follow (P→Q→R at read time) breaks snapshot
  round-trip: `rebuildMergeState` can only ever derive the *compressed* chain
  (P→R) from the persisted per-child `MergedFrom` + the child's final physical
  parent, so runtime (P→Q) and reload (P→R) disagree.

## Plan

- [x] Reproduce with a failing integration test (RED): 3-client chained merge,
  convergence AND snapshot round-trip (`TestTreeChainedMerge`)
- [x] Fix in the merge, keeping the chain flat so runtime == snapshot-reload:
  - `resolveMergeTarget`: forward children to the final live target when the
    merge lands on an already-merged-away parent (Q→R applied before P→Q)
  - stamp `MergedFrom` only on a child's first move (preserve original source
    P through the chain) so the §1.1 boundary lookup still resolves
  - re-point every source's `mergedInto` at the resolved destination, derived
    from each moved child's `MergedFrom` exactly as `rebuildMergeState` does
    (path compression) → runtime and snapshot agree
  - `FindTreeNodesWithSplitText` §1.1 redirect unchanged: the flat pointer now
    always lands on the live target in one hop
- [x] Verify convergence + snapshot round-trip (GREEN)
- [x] Regression: property matrix (1599), tree integration (161), document
  unit (243), `golangci-lint` (0 issues)
- [x] Design doc: add §6.3 + Fix 20 to `docs/design/concurrent-merge-split.md`
- [x] Self code-review (workflow, high) over the branch diff — fixed the two
  confirmed findings (MergedAt stamp-once, propagate skip vs dest)
- [x] Open PR — yorkie-team/yorkie#1906
- [x] Port the same fix to yorkie-js-sdk (`packages/sdk/src/document/crdt/tree.ts`)
  — yorkie-team/yorkie-js-sdk#1305 (RED→GREEN verified, 164 tree tests pass)

## Notes

- The persistence model forces compression: one `MergedFrom` pointer per
  child + rebuild reading the final physical parent can only ever yield P→R.
  So the runtime is made to match that (flatten at merge time) rather than
  build a multi-hop chain a reload could not reproduce. No proto change.
- Single-merge path is unchanged: `resolveMergeTarget(liveParent)` returns the
  parent itself and `MergedFrom` is nil on first move, so behavior is
  identical for the existing 69-scenario suite.
- Self-review surfaced and fixed two issues before push: MergedAt was stamped
  on every move (divergent across merge orderings) → now stamped once with
  MergedFrom; propagateMergeDeletes's concurrent-merge skip keyed on fromParent
  → now on the resolved dest.
- Known separate limitation (NOT #1304, pre-existing on main): two concurrent
  inserts at the left-most position of the *same* merged-away parent order by
  application order, not RGA CreatedAt, because §1.1's redirect returns before
  step 04's tie-break. Reproduces on a single merge too. Documented in §1.1 as
  a deferred follow-up.

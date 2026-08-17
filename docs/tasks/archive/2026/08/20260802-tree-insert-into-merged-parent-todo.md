# Tree: converge concurrent insert into a merged (removed) parent

**Created**: 2026-08-02

Fixes yorkie-team/yorkie-js-sdk#1302. Concurrent `Tree.Edit` that inserts
into a range concurrently removed by a merge diverged between replicas.

## Problem

Initial `<r><p>ab</p><p>cd</p></r>`. Concurrently:
- A: `edit(6, 6, <p/>)` — insert empty `<p>` between `c` and `d` (inside p2)
- B: `edit(0, 6)` — remove `<p>ab</p>` + p2 open tag + `c` (p2 becomes a
  merge boundary; `d` is moved up to `r`)

Replicas diverged:
- A (insert then remove): `<r><p></p>d</r>` — insert moved up with `d`
- B (remove then insert): `<r>d</r>` — insert tombstoned

Root cause: the merge left the insert's RGA anchor (`c`) behind as a
tombstone in the removed parent, so it was not in the merge target. A
late concurrent insert then targeted the removed parent and Phase 8's
parent-deletion guard tombstoned it. A first attempt (generalizing the
`FindTreeNodesWithSplitText` §1.1 redirect to non-leftmost inserts) was
abandoned: a code review proved it still diverged for multiple
concurrent inserts at the same anchor, because the redirect returned
before step 04's RGA `CreatedAt` tie-break.

## Plan

- [x] Reproduce with failing complex tests (RED): single insert AND
  three-client multi-insert (same-anchor ordering)
- [x] Fix in the merge, not the redirect: `mergeNodes` moves tombstoned
  children too (in order), preserving the source RGA sequence in the
  target so late inserts resolve normally and order via step 04's RGA
  tie-break
  - `collectBetween` collects moved children with `Children(true)`
  - add `index.Node.MoveChild`: a size-correct relocation that is
    visible-neutral for tombstones on BOTH parents (only `TotalLength`
    moves for a removed node); replaces the earlier reuse of the
    alive-node `DetachChild`/`Append` pair with a target-only undo, which
    a code review showed under-counted the source's `VisibleLength`
  - guard nil source parent in `mergeNodes` (skip untracked moves)
  - `FindTreeNodesWithSplitText` reverted to original (left-most redirect
    only); no change to the shared position resolver
- [x] Verify convergence (GREEN): single → `<r><p></p>d</r>` on both;
  multi-insert → `<r><b></b><i></i>d</r>` on all three
- [x] Two code-review rounds (workflow, high). R1 killed the redirect
  approach (same-anchor multi-insert divergence). R2 caught the
  source-side length accounting → fixed with `MoveChild`.
- [x] Regression: full tree concurrency matrix (1599), integration
  Tree/GC/Snapshot (169), whole-repo `go test ./...`, crdt unit incl.
  new element-tombstone accounting test, `gofmt`, `golangci-lint`
- [x] Open PR — merged as #1905, `Move tombstones with merge to preserve
      tree RGA anchors`
- [x] Port the same fix to yorkie-js-sdk (`packages/sdk/src/document/crdt/tree.ts`)
      — merged as yorkie-js-sdk#1303, same subject

## Notes

- Final diff: `MoveChild` in the index package + two changes in the merge
  path + a comment in `FindTreeNodesWithSplitText`. The shared position
  resolver is untouched, sidestepping the review's `to`-boundary concern.
- Residual (out of #1302 scope, unconfirmed / pre-existing): chained
  double-merge `MergedFrom` overwrite; a non-leftmost insert whose anchor
  was NOT moved because §4.3 skipped its merge; and a separate pre-existing
  ordering divergence for an insert anchored at the end of merged text.
- JS mirrors the Go structure; the same divergence reproduces there and
  needs the same merge change.

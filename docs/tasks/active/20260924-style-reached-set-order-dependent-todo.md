# A style's reached node set depends on delivery order

**Created**: 2026-09-24

Tracked as yorkie-team/yorkie#2016.

## Problem

`CRDTTree.Style` resolves its range in the *receiving* replica's visible-index
space. When another client concurrently splits or merges a `<p>`, the set of
nodes the traversal reaches depends on which change arrived first, so two
replicas render different documents. The attributes land on live nodes, so
neither side can retract and garbage collection cannot repair it.

`canStyle` was settled in #2011 so its answer cannot move. This is the other
half: the predicate is stable, the set of nodes it is asked about is not.

Base `<r><p>ab</p><p>cd</p><p>ef</p></r>`, fixed actors, only delivery order
varying:

```
A: Edit(6, 6, nil, 1)                       // split p2 between "c" and "d"
B: Style(5, 8, {"b": "x"})                  // bold p2

A,B  <r><p>ab</p><p>c</p><p>d</p><p>ef</p></r>                 live={12 288}
B,A  <r><p>ab</p><p b="x">c</p><p b="x">d</p><p>ef</p></r>     live={20 336}
```

```
A: Edit(1, 5, nil, 0)                       // delete "ab" + the boundary
B: Style(1, 6, {"b": "x"})                  // bold from inside p1 through p2

A,B  <r><p>cd</p><p>ef</p></r>              live={8 216}  gc={4 96}
B,A  <r><p b="x">cd</p><p>ef</p></r>        live={12 240} gc={8 120}
```

Exhaustive scans over that base, one structural change against one style,
both orders: 135 of 1001 split×style pairs and 297 of 7098 merge×style pairs
diverge in the rendered XML, plus 2879 merge×style pairs that differ only in
attributes on tombstones.

## Mechanism

The `fromIdx > toIdx` collapse branch in `traverseInPosRange` is not what
fires. In the split case the traversal widens correctly and reaches both
halves; the style is lost at two filters inside `Style`:

- the first rule of `styleSkipPredicate` returns `true` for the surviving left
  half's End token, on the premise that the End token is in the range only
  because a concurrent split extended it into the sibling. False here: B's
  range covered p2's End before any split existed.
- `canStyle` correctly rejects the new right half, and the compensating
  `InsNextID` walk never runs because it is gated on `index.Start` while the
  paragraph is reached through its End token.

In the merge case the nodes are not reached at all: the merge pulls the
range-end anchor back inside the range-start paragraph, so the resolved range
stops short of the End token it ran past before.

## Approach

Make the part of the reached set that is a function of the *change* stop
depending on the current tree. Both positions a style carries are stable CRDT
identities, and they answer exactly the questions the index space cannot.

- **End-token guard (§9.1)** — skip an End token only when the change's
  range-end position was declared inside that element (`endsInside`). When the
  range ran past the element's end, the End token was in it before any split.
- **Split lineage (§9.2)** — run the `InsNextID` closure from whichever token
  reaches the node first, not only from `Start`, and add the backward
  `InsPrevID` walk (`splitFamilyOf`) for the mirror case where the traversal
  reaches only the unknown right half.
- **Boundary elements (§9.5)** — `boundaryElements(from, to)` derives, from
  the two declared positions alone, the elements the change reached through a
  single token. Identical on every replica whatever a merge did to indices.
- **Range-start guard (§9.6)** — the mirror of §9.1. An element is reached
  through its End token alone exactly when this replica's range begins inside
  it; the change reached it that way only if the change's own range-start
  position was declared inside it (`beginsInside`). A merge that moves
  children into the element before them otherwise puts the style on that
  element — a live node the other order never touches.
- **Shared resolution** — `styleTargets` now does the whole resolution once
  and hands `Style` and `RemoveStyle` one ordered, duplicate-free node list.

## Plan

- [x] Reproduce both cases through the protobuf converter, without a server
- [x] Rebuild the issue's exhaustive scans and confirm the numbers match
- [x] §9.1: `endsInside` gate on the End-token guard
- [x] §9.2: token-agnostic forward closure + backward `splitFamilyOf`
- [x] §9.5: `boundaryElements` from the change's declared positions
- [x] §9.6: `beginsInside` guard for a range start a merge moved
- [x] Treat an empty resolved range as collapsed in `reversedFromAnchorRecovery`
- [x] Fold `Style`/`RemoveStyle` onto one `styleTargets` resolution
- [x] Confirm no pair that converged before diverges after
- [x] Regression tests in `pkg/document/tree_style_reached_set_test.go`,
      covering `RemoveStyle` as well as `Style`
- [x] Design doc: §9.1, §9.2, new §9.5, known limitations, Fix 25
- [ ] Port to the JS SDK so snapshots and clients agree — **blast radius
      of landing Go-only is recorded under "Known limitation" below and in
      `docs/design/concurrent-merge-split.md` (Cross-implementation)**

## Result

| scan | pairs | rendered | tombstone-only |
|---|---|---|---|
| split × style | 1001 | 135 → 0 | 0 → 0 |
| merge × style | 7098 | 297 → 0 | 2879 → 1292 |
| randomised sweep | 300 seeds | 11 → 9 | 47 → 30 diverging seeds |

Scanned again with `RemoveStyle` in place of `Style` over a pre-bolded base:
identical on every count, split and merge families closed in the rendered
document. That is the point of the shared `styleTargets` — the two operations
cannot drift.

**Every rendered divergence the issue reported is closed.** What remains is
1292 pairs whose two orders render the same document but book attributes onto
a different set of tombstones. §9.6 closed the last 126 rendered ones, which
were the only family still writing to a LIVE node, and with them the §9.4
known limitation recorded as the `RemoveStyle(6,8)` / `Edit(1,5)` PBT
counterexample.

No pair or seed that converged before diverges after.

## Known limitation

**The JS SDK is not ported.** §9.1, §9.2 and §9.5 move which nodes a
`Tree.Style`/`Tree.RemoveStyle` reaches, in the Go CRDT only.
`server/packs/snapshot.go` rebuilds snapshots through this code, so until the
port lands a JS client and the server resolve different reached sets for the
concurrent split/merge shapes here, and a JS client only picks up the server's
answer when it reloads from a snapshot.

Landing Go-first is deliberate. Every one of those shapes already diverged
*between two Go replicas* by delivery order — 135 split×style and 297
merge×style rendered divergences on the scan base — which is the worse
failure, because it is two replicas of the same implementation disagreeing
with no way back. Holding the Go fix for the port keeps that. The two
implementations were never in agreement to preserve: both were
order-dependent, so "agreement" meant agreeing only when the orders happened
to match.

Attributes on tombstones. A *removed* element a style covered strictly
between its two anchors is still resolved in the current index space, and a
merge can move it out of reach, so the two orders leave a different amount of
attribute metadata on removed nodes — 1292 of the merge scan's 7098 pairs,
none of them rendering differently. Closing that needs the range resolved in
an index space filtered by the change's version vector, which has to land on
the server and the JS SDK together.

The issue also reports two randomised seeds where one interleaving fails to
apply (`node not found`) while another succeeds. Not reproduced here and not
addressed; it may have an independent cause.

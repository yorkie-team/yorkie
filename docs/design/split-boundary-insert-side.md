---
title: split-boundary-insert-side
target-version: 0.7.26
---

# Which Side of a Split Boundary an Insert Lands On

## Problem

Text inserted at exactly the position where a peer concurrently splits the
paragraph lands on the left of the new boundary on one replica and on the right
on the other (yorkie-js-sdk#1436). Three operations are enough; both replicas
hold `<doc><p>ab</p></doc>`:

```go
d1: tree.Edit(2, 2, &json.TreeNode{Type: "text", Value: "e"}, 0) // "aeb"
d1: tree.Edit(2, 2, nil, 1)                                      // split "a|eb"
d2: tree.Edit(2, 2, nil, 1)                                      // split "a|b"
// d1: <doc><p>a</p><p></p><p>eb</p></doc>
// d2: <doc><p>a</p><p>e</p><p>b</p></doc>
```

The replica that applied the insert first resolves the split against a tree
where the text is already there. The replica that applied the split first has
the right half in the product, so the insert resolves at the end of the left
piece. Each is self-consistent; the two disagree.

Two rules of [concurrent-merge-split](concurrent-merge-split.md) pull against
each other here. §7.3 (Boundary Insert Migration) keeps a concurrent insert at
a split boundary on the *left* of it. §7.8 orders same-boundary split products
newest ticket first. Where a concurrent product begins with inserts the
splitter never saw, both cannot hold.

yorkie-js-sdk#1467 resolves this in the JS SDK. Where a same-boundary split
lands is a replicated contract -- the server and every SDK must pick the same
node for the same change -- so this document records the Go port of the same
two rules, made for parity with that PR at `89b0b2a8`.

### Goals

- The minima of yorkie-js-sdk#1436 converge in Go, in tree shape (node IDs)
  and in XML, with the same converged XML the JS SDK produces.
- No regression in the existing split-ordering and split-cascade suites.

### Non-Goals

- Changing where a *range* endpoint resolves. Phase 2's
  `advancePastUnknownSplitSiblings` (§7.5) stays the only rule that moves range
  endpoints past split products; style targets (§9.4) and delete ranges are
  untouched.
- Departing from the JS rule, even where it is known to be wrong. See
  [Known gap](#known-gap-a-trailing-tombstone).

## Design

Both rules live in `pkg/document/crdt/tree.go`, named after their JS
counterparts, and are two views of one claim: **when the right piece of a
concurrent split begins with a run of inserts the splitter did not know, the
two boundaries are not the same boundary**, so §7.8's ticket order does not
decide between them -- content order does.

### Split applied second (`orderSameBoundarySplit`)

§7.8 walks the `InsNextID` chain to order same-boundary products by ticket.
When the product sitting next is older than the split but begins with
concurrent inserts, the split lands *inside* that product, past the run,
instead of in front of it. When the walk steps over newer products, the last
of them is split past its own run the same way, rather than at offset 0.

`boundaryInsertRunOf` counts the run: *text* children at the start of a node
created by a change outside the editor's version vector. Text split siblings
carry their original's ticket and so end the run by being known, and the first
element child ends it. That is the same set of children
`advanceIntoSplitProducts` crosses on the other side, so the two rules agree on
how long the run is. Text-only is also the only GC-stable measure: telling an
element insert from an element split product would need `InsPrevID`, which
`Tree.Purge` relinks and clears and `DropSplitLinks` drops on a copy.

### Insert applied second (`advanceIntoSplitProducts`)

The step 04 RGA scan of `FindTreeNodesWithSplitText` stops at the last child of
the resolved parent, but the sequence continues into a concurrent split product
of that parent. `advanceIntoSplitProducts` carries the scan on there, over a
run of newer-ticket *text* children -- the same rule step 04 applies inside one
node. Element children at that boundary are §7.8's business; crossing them made
the two rules disagree in the JS SDK and broke eight split-ordering tests.

"The last child" is measured in live content (`atEndOfLiveContent`), not in all
children, following the JS rule.

It runs in `Edit` right after Phase 1, on a **collapsed** range only, not
inside `FindTreeNodesWithSplitText`: that method also resolves style and
delete ranges, and an endpoint that walked into a product would change which
parents `collectBetween` runs between, over nodes the editor never saw.

### Known gap: a trailing tombstone

`atEndOfLiveContent` skips tombstones, and whether a node is a tombstone when
the insert arrives depends on delivery order. Take `<p>acb</p>`: d1 inserts
`r` after `c` and splits right after `c`; d0 inserts `u` between `a` and `c`;
d2 removes `c`. A replica that applies the removal before the insert sees `a`
as the end of live content, advances, and carries `u` past `c` into the
product; the typist, which applied its insert while `c` was live, keeps it in
the paragraph:

```
typist:            <doc><p>au</p><p>rb</p></doc>
removal first:     <doc><p>a</p><p>rub</p></doc>
```

The JS PR at `89b0b2a8` diverges in exactly the same way, with the same node
shapes, and both bases converge (on `<p>au</p><p>rb</p>`). This is open review
finding (a) on yorkie-js-sdk#1467. The port keeps the rule as JS has it, since
a Go replica that disagreed with a JS one would diverge on every ordering;
`TestTreeInsertAtSplitBoundaryPastRemovedChild` records the case as a skipped
subtest so the fix lands in both SDKs together. Finding (b) -- that
`orderSameBoundarySplit` does not apply the live-content rule -- is ported as is
and not separately tested here.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A JS replica and a Go/server replica place the same split differently | This port; the five JS cases are translated one-for-one into `tree_boundary_insert_side_test.go` with the converged XML asserted |
| The trailing-tombstone case above diverges where `main` converged | Documented and kept as a skipped subtest; the fix has to change both SDKs at once |
| An `InsNextID` that did not come from `SplitElement` redirects a split or an insert | `sharesSplitFamilyParent` keeps both walks inside one split family; `insNextWalker` stops a cyclic chain |
| Splitting a tombstoned sibling makes the product born tombstoned | Both walks stop at a removed sibling, as §7.8 already did |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Port the JS decisions exactly, known gap included | Parity is what convergence across SDKs needs; a Go-only fix is itself a divergence |
| Cross text children only, in both rules | Element children at that boundary are §7.8's, and text is the only GC-stable measure of the run |
| Apply the insert-side rule to a collapsed range in `Edit` | A range endpoint moving into a product would widen or shorten what the edit deletes and merges |
| No split-position measurement change | The JS PR also moves where the split's `TreeChange` position is measured; the Go tree emits no such change, so there is nothing to port |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Leave Go on §7.8 ticket order | A JS replica on the new rule and a Go/server replica would place the same split differently |
| Fix the trailing-tombstone gap in Go only | Same reason, and the JS fix is still under review |
| Resolve inside `FindTreeNodesWithSplitText` for every range | Style and delete ranges resolve through it, and their endpoints must not follow content across a boundary |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

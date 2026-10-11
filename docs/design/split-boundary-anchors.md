---
title: split-boundary-anchors
target-version: 0.7.25
---

# Split Boundary Anchors

## Problem

Text typed at the exact point where a peer concurrently splits the
paragraph lands on the left of the new boundary on one replica and on the
right on the other (yorkie-js-sdk#1436). In an editor this is one user
typing while another presses Enter at the same spot.

The tree's shape depends on arrival order:

- A split moves a suffix of children into a new product placed after the
  node it splits (the `InsNextID` chain).
- An insert resolves to (parent, left sibling) and runs its RGA skip only
  inside `parent`'s children.
- So on a replica that applied the split first, the insert never compares
  itself with the split. On a replica that applied the insert first, the
  split partitions children by local offset and never compares tickets.

§7.3, §7.4, §7.5 and §7.8 of [concurrent-merge-split.md](concurrent-merge-split.md)
each correct one projection of this. Three patch rounds that added more
rules of the same kind (yorkie#2171, yorkie-js-sdk#1467) moved divergences
instead of removing them. A two-replica insert+split fuzz from `<p>ab</p>`
diverged on 1068 of 5000 seeds on main. The best combination of those rules
still diverged on 407.

### Goals

- Every interleaving of concurrent text inserts, splits and in-paragraph
  deletes on a paragraph of text converges, in XML and in node identity,
  with GC and snapshot reloads.
- Undoing and redoing those splits does not diverge where main converges.
- Concurrent text keeps landing on the left of a concurrent Enter, as it
  does today.

### Non-Goals

- Merge ‖ split divergence in general (yorkie-js-sdk#1474). It is the
  largest class on main and needs its own design. This change touches
  merges only where undoing a split needs it (see Undo and merges).
- Splits whose innermost node holds element children. They keep the
  existing rules. See Scope.

## Design

### The sequence model

Treat the text of a split family (a node and the products split off it) as
one RGA sequence. A split is a **boundary** in that sequence, anchored right
before R, the first item after its position that its creator knew: a text
node, another boundary, or the end of the family.

- Text concurrent with the split that lands between the position and R
  stays on the left of the boundary (§7.3's intent).
- Boundaries anchored before the same R are siblings, ordered newest first
  (§7.8's intent). Each owns its emission: the boundaries anchored before
  it and its group, meaning everything newer than it.
- A split made inside the group of a boundary anchored before the same R
  **nests** under it, and is ordered among that boundary's nested ones.

The result is a function of the set of items alone, so every delivery order
gives it. An order-independent oracle built on this model reproduces main's
results exactly, and was used to check every step of the exploration's fuzz.

### What a product records

The innermost level of a split, the one that divides the node holding the
position, records a **boundary record** on its product when that node's
children are all text:

| Part | Meaning |
|---|---|
| anchor | The ID of R, or the end of the family |
| nest | The same-anchor boundary this one nests under, if any |

`placeSplitBoundary` places a split, and `advanceAcrossBoundaries` resolves
the anchor of a pure insert. Both read these records instead of comparing
tickets across products. Without the record, a replica cannot tell an
unknown boundary anchored before a known one from a same-anchor sibling.
That is why rules over the current tree shape could not close the issue.

In memory the record lives on `TreeNode` (`BoundaryAnchor`,
`BoundaryAtEnd`, `BoundaryNest`), and `Tree.anchoredBefore` indexes products
by anchor ID.

### Scope

The rules apply when the split node holds no element child that the editing
change saw. An element whose removal the change knew does not count. Outer
levels of a multi-level split, and splits of nodes with element children,
fall back to `orderSameBoundarySplit`, extended with:

- the insert-side run of yorkie#2171 (`boundaryInsertRunOf`);
- the walk stop at the right half of yorkie#2098 (`holdsKnownChild`).

The walk over a family follows only products that carry a record
(`nextBoundaryNode`). Their current children are not consulted, because
those change as elements are removed and purged, and the walk also runs
during GC.

### Undo and merges

Undoing a split merges its product away. These cases come up when that
races a peer's split, and each would otherwise diverge where main
converges:

- **A boundary the change saw removed is not in its sequence.** The tail
  walk passes over it and what is left in it, so it is never R. Its
  tombstone sits wherever its removal left it, which differs between
  replicas. The same holds for nesting, which skips boundaries the change
  saw removed.
- **A merge the change did not know may have moved the position.**
  `seenParentOf` reads `MergedFrom` on the children around the position to
  find the node the change saw as the parent. Nesting is decided from
  there, and the tail walk passes over that node, whose boundary was behind
  the position for the change.
- **A split must not open inside a removed product it did not know.** It
  would mint a product born removed, and the paragraph would be lost on one
  replica. `liveSplitTarget` opens it at the end of the nearest live node
  before that product in the family. The merge target is not used, because
  later splits may have carried the moved content past it.
- **Merging a product joins it to the boundary before it.** When a merge
  removes an anchored product, products the merging change did not know
  may have opened between the node it names and the product.
  `boundaryMergeTarget` moves the children into the nearest such product
  instead of the named node. A replica that applied the merge before those
  splits put the content in the named node, and the splits then carried it
  on into their products. **This changes what a merge produces** in that
  concurrent case.

### Garbage collection

- **Relink.** Purging the node an anchor names moves the anchor to the next
  item of the sequence. A text node's next item is its next sibling or the
  next boundary. A purged product's children go with it, so its next item is
  the next boundary. GC visits its pairs in map order, so every order of one
  pass has to meet: products are relinked as well as text, and purging an
  element relinks the anchors in its whole subtree, descendants first,
  while the subtree is still in the tree.
- **Barrier.** A text tombstone that is the last child of its parent is the
  last item before the next boundary in the sequence. Its purge waits until
  that boundary's ticket is stable, through `PurgeBarrierAt`. Without the
  barrier the fuzz diverged on 4–11 of 5000 seeds with deletes.
- **Index.** `NewTree` rebuilds `anchoredBefore`. `setBoundary` keeps it in
  step, a purge drops the purged subtree from it, and dropping the records
  of an element payload clears it.

### Persistence

The records are permanent node state. A later split, causally after a
boundary, reads that boundary's anchor to decide whether it nests under it.
The exploration tested dropping records once a split was causally stable:
it diverged on 18–81 of 5000 seeds.

Snapshots carry them on the tree, not on `TreeNode`:

```protobuf
message JSONElement {
  message Tree {
    ...
    repeated TreeBoundary boundaries = 6;
  }
}

message TreeBoundary {
  TreeNodeID product = 1;
  TreeNodeID anchor = 2;  // absent: the end of the split family
  TreeNodeID nest = 3;    // absent: nests under no other boundary
}
```

`TreeNode` is also the message of operation content, so a record on it
could arrive from a peer. On the tree, content cannot carry one at all.
Element payloads and undo copies drop their in-memory records, since they
are copies of live nodes that leave their family.

Operations do not change: each replica derives the record when it applies
the split, from the position and the change's version vector.

### Measurements

Go, two and three replicas from `<p>ab</p>`. Every version runs the same
seeds and is judged by XML and live node identity.

Broadened fuzz, 1500 seeds per cell (no GC). Cells are 2 / 3 replicas,
with regressions against main in parentheses:

| Ops | main | main + #2098 | V8 (#2171) | This design |
|---|---|---|---|---|
| insert+split | 267 / 590 | 154 / 353 (0/0) | 195 / 498 (1/2) | 0 / 0 |
| +delete | 157 / 357 | 99 / 215 (0/0) | 112 / 290 (2/4) | 0 / 0 |
| +style | 130 / 328 | 69 / 139 (0/0) | 92 / 275 (0/0) | 0 / 0 |
| merge | 973 / 1272 | 958 / 1258 (2/0) | 955 / 1258 (0/0) | 848 / 1145 (4/0) |
| all | 677 / 968 | 654 / 936 (0/0) | 657 / 949 (1/1) | 540 / 785 (6/3) |

GC, snapshot and undo. Cells are runs that did not converge, with
regressions against main in parentheses:

| Case | Runs | main | main + #2098 | V8 (#2171) | This design |
|---|---:|---:|---:|---:|---:|
| GC every 3 steps | 2000 | 1046 | 848 (1) | 872 (7) | 0 |
| + snapshot reload every 7 | 2000 | 1045 | 846 (1) | 868 (7) | 0 |
| Undo and redo, seeds 1–600 | 1200 | 463 | 427 (2) | 405 (3) | 237 (3) |
| Undo and redo, seeds 601–1800 | 2400 | 896 | 828 (4) | 827 (6) | 501 (5) |

The undo rules above were developed on seeds 1–600. Seeds 601–1800 were
held out, and the regression rate on them matches. In those runs, 12 and 39
runs converge in XML and node identity but end with different boundary
records. A later concurrent split at the same place could then diverge.

`TestTreeBoundaryFuzz` replays 300 seeds on two and three replicas with GC
and snapshot reloads. main fails 332 of those 600 runs, and this design
passes all of them. `TestTreeBoundaryUndoSplit` pins the undo cases.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Old clients and servers apply the old rules and ignore `boundaries`, so they disagree with new ones in these concurrent cases. The server builds snapshots with its own rules | Release the server and SDKs together, as for any op-semantics change. Old snapshots load with no records, and their products keep the old rules on every replica alike. No version gate prevents a mixed deployment |
| A merge now moves an anchored product's content into a concurrent product rather than the node it names | Documented here and in the release note. It removes more merge divergences against main than it adds (merge fuzz: 127–186 runs fixed, 4/0/6/3 regressions) |
| Undo, redo and concurrent splits still regress against main in 0.2–0.3% of the undo fuzz, and boundary records differ in 1–2% | Measured and pinned. The general fix is yorkie-js-sdk#1474: merges modelled as removing a boundary from the sequence |
| Two concurrent removals of the same element: `removedAt` keeps the later one, so a change that knew only the first counts the element as present | The same limitation as the existing `seenGone` check in `appendUnknownSplitSiblings` |
| Splits outside the scope keep diverging | Unchanged from today; the fallback rules are a superset of main's |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Anchor records rather than more §7.x rules | Three rounds of rules each moved divergences. The tree cannot tell the two cases apart without the record |
| Keep "concurrent text stays left" | Today's behavior. The alternative that orders an Enter by ticket, as Yjs and Automerge do for `\n`, changes 15–60% of concurrent results |
| Persist the records for the life of the node | Later splits read them; dropping them once stable diverged |
| Records on the tree message, not on `TreeNode` | Operation content cannot carry them, and no state pairs an anchor with the end |
| Version-vector scope check, record-based family walk | Every input to a decision must read the same on every replica. Local tombstones and GC timing do not |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| The split family as one RGA sequence, with products as markers ordered by ticket | No wire change, and fewer rules. But concurrent text can move to the line after an Enter, and the prototype still diverged on nested splits, OpInfo and undo (undo fuzz: 80 and 181 regressions against main) |
| Records as three fields on `TreeNode` | Operation content shares the message, so four paths had to strip them, and an anchor with the end flag was representable |
| YATA/Fugue-style left and right origins on every node | Same wire cost as the records, and a larger change |
| Full flattening of the tree into block markers | A new Tree CRDT |

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
- No interleaving that converges today diverges.
- Concurrent text keeps landing on the left of a concurrent Enter, as it
  does today.

### Non-Goals

- Merge ‖ split divergence (yorkie-js-sdk#1474). It is the largest class on
  main and needs its own design. This change leaves merges on the existing
  rules.
- Splits at level ≥ 2 or of nodes holding element children. They keep the
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
results exactly, and was used to check every step of the fuzz below.

### What a product records

A level-0 split of a node whose children are all text records on its
product:

| Field | Meaning |
|---|---|
| `BoundaryAnchor` | The ID of R, when R is a node |
| `BoundaryAtEnd` | R is the end of the family |
| `BoundaryNest` | The same-anchor boundary this one nests under, if any |

`placeSplitBoundary` places a split, and `advanceAcrossBoundaries` resolves
the anchor of a pure insert. Both read these records instead of comparing
tickets across products. Without the record, a replica cannot tell an
unknown boundary anchored before a known one from a same-anchor sibling.
That is why rules over the current tree shape could not close the issue.

### Scope

The rules apply when the split node holds no element child that the editing
change saw. An element whose removal the change knew does not count. Other
splits fall back to `orderSameBoundarySplit`, extended with:

- the insert-side run of yorkie#2171 (`boundaryInsertRunOf`);
- the walk stop at the right half of yorkie#2098 (`holdsKnownChild`).

The walk over a family follows only products that carry a record
(`nextBoundaryNode`). Their current children are not consulted, because
those change as elements are removed and purged, and the walk also runs
during GC.

### Garbage collection

- **Relink.** Purging the node an anchor names moves the anchor to the next
  item of the sequence. A text node's next item is its next sibling or the
  next boundary. A purged product's children go with it, so its next item is
  the next boundary. Products are relinked as well as text. One GC pass can
  purge both in either order, since pairs are visited in map order, and the
  two orders have to meet.
- **Barrier.** A text tombstone that is the last child of its parent is the
  last item before the next boundary in the sequence. Its purge waits until
  that boundary's ticket is stable, through `PurgeBarrierAt`. Without the
  barrier the fuzz diverged on 4–11 of 5000 seeds with deletes.
- **Index.** `Tree.anchoredBefore` maps an anchor ID to its products, so a
  purge touches only those. `NewTree` rebuilds it. `setBoundary` keeps it in
  step, and a purge drops the purged subtree from it.

### Persistence

The records are permanent node state. A later split, causally after a
boundary, reads that boundary's anchor to decide whether it nests under it.
The exploration tested dropping records once a split was causally stable:
it diverged on 18–81 of 5000 seeds. So `TreeNode` in `resources.proto`
gains three fields, written to and read from snapshots:

```protobuf
TreeNodeID boundary_anchor = 11;
bool boundary_at_end = 12;
TreeNodeID boundary_nest = 13;
```

Operations do not change: each replica derives the record when it applies
the split, from the position and the change's version vector. Operation
content, element payloads and undo copies drop the records along with the
split links. A peer controls those bytes, and none of those nodes is a
split product.

### Measurements

From the exploration, in JS (two and three replicas from `<p>ab</p>`). The Go
port gave identical per-seed results on the same scripts.

| Harness | main | This design |
|---|---|---|
| Broadened fuzz, 3000 seeds × 24 steps: insert+split, +delete, +style | 533 / 1217, 316 / 735, 261 / 677 | 0 |
| Oracle harness, 5000 seeds × 30 steps, GC every 3 steps and snapshot reload every 7, with and without deletes | 906–2571 | 0 |

The cells show the 2 / 3 replica results. Among seeds where main also
converges, this design's converged result differs from main's on 0–2%.
The repository test `TestTreeBoundaryFuzz` replays 300 seeds on two and
three replicas with GC and snapshot reloads. main fails 332 of those 600
runs, and this design passes all of them.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Old clients and servers apply the old rules and ignore the fields, so they disagree with new ones in these concurrent cases | Release the server and SDKs together, as for any op-semantics change. Old snapshots load with no records, and their products keep the old rules on every replica alike |
| A merge tombstones a boundary that an anchor or a nest names. Replicas purge it at different times, and `boundaryNestOf` scans tombstoned siblings | Not reproduced. It lies in merge ‖ split, which is already broken (yorkie-js-sdk#1474) and will be revisited there |
| Two concurrent removals of the same element: `removedAt` keeps the later one, so a change that knew only the first counts the element as present | The same limitation as the existing `seenGone` check in `appendUnknownSplitSiblings` |
| Splits outside the scope keep diverging | Unchanged from today; the fallback rules are a superset of main's |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Anchor records rather than more §7.x rules | Three rounds of rules each moved divergences. The tree cannot tell the two cases apart without the record |
| Keep "concurrent text stays left" | Today's behavior. The alternative that orders an Enter by ticket, as Yjs and Automerge do for `\n`, changes 15–60% of concurrent results |
| Persist the records for the life of the node | Later splits read them; dropping them once stable diverged |
| Version-vector scope check, record-based family walk | Every input to a decision must read the same on every replica. Local tombstones and GC timing do not |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| The split family as one RGA sequence, with products as markers ordered by ticket | No wire change, and fewer rules. But concurrent text can move to the line after an Enter, and the prototype still diverged on nested splits, OpInfo and undo. Measured in second-brain's `split-boundary-representation` brief |
| YATA/Fugue-style left and right origins on every node | Same wire cost as the records, and a larger change |
| Full flattening of the tree into block markers | A new Tree CRDT |

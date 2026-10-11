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
- **A merge the change did not know may have moved the position.** The
  operation names the parent it saw. When a merge the change did not know
  has taken that node away, `seenParentOf` uses it: nesting is decided from
  there, and the tail walk passes over that node, whose boundary was behind
  the position for the change. The children around the position cannot tell
  this case apart, because the end of the merge target and the start of the
  merged-away node have the same neighbours once the merge has moved them.
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
- **Text stays left of a boundary split off after a merge it missed.** An
  insert at the start of a node a merge took away is forwarded to where the
  merge put that node's first child. If a later split has carried that child
  into a new product, the insert did not know that split either, so
  `preMergeHolder` sends it to the end of the node before the product, where
  a replica that applied the insert first left it. It does so only when the
  product is anchored before that moved child and its ticket is above the
  merge's. Text typed into the product after it was created may stand in
  front of the moved child; it is newer than the product, so the insert did
  not know it, and the moved child still counts as opening the product. The
  ticket order alone is not causality: a split that did not know
  the merge and opened at the end of the merge target is anchored before the
  merged-away node, and the insert joins its paragraph.
- **A split that missed a merge opens in front of the merged-away node.**
  When the split position is followed by the run a merge the change did not
  know moved into that node, the change saw the merged-away node there.
  `unknownMergeBoundaryAt` anchors the split before that node and opens it in
  front of the run, which stays in the product, so the replica that applied
  the merge first records what the one that applied the split first does.
  The moved child must open the run: when a split the change knew has
  divided the merged-away node's content, the position was inside that node
  for the change, not before it. Tombstones count as moved children: an
  undo can revive one, so whether it is still deleted depends on what a
  replica has applied, not on the splitting change. Whether the change knew the merge is tested against every merge ticket of
  the node (see below).
- **What stays ambiguous.** A product split after the merge by a change that
  knew it, and a concurrent split anchored before the same moved child that the
  merge then moved its content into, leave the same tree, records and ticket
  order. Only the splitting change's version vector tells them apart, and the
  tree does not keep it. `preMergeHolder` treats both as post-merge.
- **Merge forwarding follows the moved children.** A snapshot rebuilds a
  merge source's `mergedInto` from the parent of its first moved child.
  When a split carries every child a merge moved out of a node into a
  product, `followMovedMergeChildren` repoints the runtime pointer the same
  way. Other moves are not followed: a purge of the first moved child, or a
  merge that moves the children again, can still leave the runtime pointer
  and a reload's apart.
- **Two merges of one node keep the smaller ticket.** When two replicas
  remove the same boundary concurrently, the first merge to arrive moves the
  children and stamps `MergedAt`; the second finds the node already merged
  away. A cross-node range edit whose end names such a node lowers the moved
  children's `MergedAt` to the smaller ticket (`lowerRepeatedMergeTickets`),
  so every replica holds the same value once both merges arrive. A later
  split may have known only the merge with the larger ticket, so the node
  also keeps every merge ticket (`mergeTickets`), and a change knew the node
  was merged away when it knew any of them (`knewMergeOf`, `knewRemovalOf`).
  Every merge a change knew has arrived wherever it is applied, so the answer
  does not depend on which other merges have. Without the list, the Enter of
  three replicas' concurrent Backspaces anchored before the merged-away node
  or before the moved text depending on what a replica held when it applied
  the split.

`TestTreeBoundaryKnownDivergences` records the case this leaves, as a
skipped test that `YORKIE_RUN_KNOWN_DIVERGENCES` runs.

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
    repeated TreeMerge merges = 7;
  }
}

message TreeBoundary {
  TreeNodeID product = 1;
  TreeNodeID anchor = 2;  // absent: the end of the split family
  TreeNodeID nest = 3;    // absent: nests under no other boundary
}

message TreeMerge {
  TreeNodeID source = 1;           // a node more than one merge removed
  repeated TimeTicket tickets = 2; // every such merge, in ticket order
}
```

A `TreeMerge` exists only for a node that two or more merges removed
concurrently, and only while that tombstone lives: GC drops it with the
node.

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
| merge | 973 / 1272 | 958 / 1258 (2/0) | 955 / 1258 (0/0) | 823 / 1130 (4/0) |
| all | 677 / 968 | 654 / 936 (0/0) | 657 / 949 (1/1) | 518 / 764 (5/4) |

This fuzz compares tombstones too. One of the four three-replica `all`
regressions (seed 322) differs only in the order of a live character and a
deleted one that a merge moved next to it. Its minimized script orders them
differently on main as well; main's run of the full seed happens not to
reach it.

GC, snapshot and undo. Cells are runs that did not converge, with
regressions against main in parentheses:

| Case | Runs | main | main + #2098 | V8 (#2171) | This design |
|---|---:|---:|---:|---:|---:|
| GC every 3 steps | 2000 | 1046 | 848 (1) | 872 (7) | 0 |
| + snapshot reload every 7 | 2000 | 1045 | 846 (1) | 868 (7) | 0 |
| Undo and redo, seeds 1–600 | 1200 | 463 | 427 (2) | 405 (3) | 222 (5) |
| Undo and redo, seeds 601–1800 | 2400 | 896 | 828 (4) | 827 (6) | 460 (4) |
| Undo and redo with snapshot reloads, seeds 1–1800 | 3600 | 1066 | – | – | 509 (5) |
| Backspace-heavy, three replicas | 3000 | 491 | – | – | 241 (21) |
| + snapshot reloads | 3000 | 498 | – | – | 242 (20) |

In the reload runs a reloaded replica only pulls afterwards: a reload bumps
the replica's Lamport clock, and letting it issue more edits would change
every later tie-break, so the run would measure that instead of the
snapshot state. A targeted set of twelve scripts, an undo racing a peer's
split and a third replica's typing in every delivery order with and
without a ticket bump, converges in all twelve (main: none).

The undo rules above were developed on seeds 1–600 and checked on seeds
601–1800, which were held out: 5 of 1200 (0.42%) and 4 of 2400 (0.17%)
regress. In those runs, 11 (0.92%) and 25 (1.04%) runs converge in XML and
node identity but end with different boundary records. A later concurrent
split at the same place could then diverge. In the Backspace-heavy runs,
which press Backspace at joins concurrently, 29 runs end with different
records; before every merge ticket was kept (`mergeTickets`) it was 66.
At runtime main keeps merge forwarding where the merge put it, while its
snapshots rebuild it from where the children are. This design follows
split-moved children at runtime too, which some regressions against main's
runtime behaviour follow from.

#### What the regressions are

A regression is a run that converges on main and does not here. The
verdict reads XML first, then live node identity; boundary records are left
out of the count. The 11 regressions in the two undo runs without reloads,
as measured at 3a0e81e2 (9 now):

- **All are real divergence.** 7 differ in XML, which users see: text lands
  in different paragraphs. 4 differ only in which node holds an empty
  paragraph, which the next edit there exposes, since operations name nodes
  by ID.
- **All are the same scenario on both sides.** Each version generates its
  own script from its own state, so a per-seed comparison could pit
  different scripts against each other. Replaying this design's 11 scripts
  literally on main converges every time.
- **main's convergence on them is fragile.** Inserting one snapshot reload
  of any replica at any point of the same script made main diverge in 2–68%
  of the placements, 10–44% for most (measured at bd66c1bc). Those reloads let the replica keep
  editing, so part of that is a changed tie-break rather than snapshot
  state; the reload runs above avoid it.
- **Every one involves undoing or redoing a split.** Minimized, they take
  5 to 15 operations (at bd66c1bc). The shortest ones traced undo or redo an Enter at a
  paragraph start while a peer edits the paragraph it made.

`TestTreeBoundaryFuzz` replays 300 seeds on two and three replicas with GC
and snapshot reloads. main fails 332 of those 600 runs, and this design
passes all of them. `TestTreeBoundaryUndoSplit` pins the undo cases.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Old clients and servers apply the old rules and ignore `boundaries`, so they disagree with new ones in these concurrent cases. The server builds snapshots with its own rules | Release the server and SDKs together, as for any op-semantics change. Old snapshots load with no records, and their products keep the old rules on every replica alike. No version gate prevents a mixed deployment |
| A merge now moves an anchored product's content into a concurrent product rather than the node it names | Documented here and in the release note. Merge fuzz against main: fewer divergences, 4/0/5/4 regressions |
| Snapshots gain `TreeMerge`, read only by new servers and SDKs | Present only for a node that concurrent merges removed, dropped with it by GC. A snapshot without it loads with the smaller ticket alone, as before it existed |
| Undo, redo and concurrent splits still regress against main in 0.17–0.42% of the undo fuzz, and boundary records differ in about 0.9–1.04% | Measured; the shortest cases are pinned |
| One known case (`TestTreeBoundaryKnownDivergences`): a split racing a peer's split and undo with typing, which leaves the same state as a case that needs the text on the other side (diverges on main too) | Keeping, per split product, whether its change knew the merges of the children it carried would settle it, at the cost of new persisted state. The general fix is yorkie-js-sdk#1474 |
| Two concurrent removals of the same element: `removedAt` keeps the later one, so a change that knew only the first counts the element as present | Covered for repeated merges by `mergeTickets`. Still open for plain deletes, an empty node merged away twice (no moved child to stamp), and a node one range delete removes while a Backspace merges it. The same limitation as the existing `seenGone` check in `appendUnknownSplitSiblings` |
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
| Inferring the parent a split saw from the neighbours' `MergedFrom` | The end of a merge target and the start of a merged-away node share neighbours; the operation's declared parent does not |
| Records as three fields on `TreeNode` | Operation content shares the message, so four paths had to strip them, and an anchor with the end flag was representable |
| YATA/Fugue-style left and right origins on every node | Same wire cost as the records, and a larger change |
| Full flattening of the tree into block markers | A new Tree CRDT |

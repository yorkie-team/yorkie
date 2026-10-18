---
title: document-size-limit
target-version: 0.7.24
---

# Document Size Limit

> **Status: implemented.** The server enforces `MaxSizePerDocument` on push
> with the lagging, growth-only gate described below.

## Problem

`MaxSizePerDocument` is a per-project quota on a document's size
(`server/backend/database/project_info.go`, default 10 MiB). It is sent to the
client in the attach response (`server/rpc/yorkie_server.go` →
`client/client.go`) and enforced there: `Document.Update` compares the clone's
`resource.DocSize.Total()` against `MaxSizeLimit` and refuses the local update
with `ErrDocumentSizeExceedsLimit` (`pkg/document/document.go`).

Before this design, the server never re-checked it. `pushPack` filtered
already-pushed changes, validated clientSeq continuity, serverSeq ordering and
epoch, and handed the remainder to `CreateChangeInfos`
(`server/packs/pushpull.go`). No branch on that path read the document's size,
because no branch on that path had a document at all.

So the quota was advisory. A client that does not run the check — a modified
SDK, or a direct Connect call — could grow a document without bound, and the
only things in the way were `maxRequestBytes` (`server/rpc/server.go`), which
bounds one push and not the total, and MongoDB's 16 MB BSON limit on the
snapshot (see [snapshot-overflow.md](snapshot-overflow.md)), which bites long
after the quota was meant to.

### Goals

- Bound the size of a document held on the server regardless of client behavior.
- Keep the common push path free of whole-document rebuilds.
- Never wedge a document that is already over quota: a client must always be
  able to shrink it, detach from it or remove it.

### Non-Goals

- Byte-exact agreement between the server's bound and the client's check. The
  client is the fast, exact gate; the server gate is the backstop.
- Replacing the client-side check. It stays: it is what gives an honest client a
  synchronous error at the edit that would exceed the quota, before the edit is
  applied locally.

## Design

The gate has two halves: **where the number comes from** and **what refusing
does to the client**. Both are decided below.

### Where the number comes from

The only way to obtain a document's size server-side is to build the
document: `BuildInternalDocForServerSeq` (`server/packs/snapshot.go`) — a
snapshot load, a range query over changes, an `ApplyChangePack`, a
`GarbageCollect` and two whole-document `DeepCopy` calls. Putting that in
`pushPack` would run it on every push, which is not acceptable.

So the gate **lags**: the path that already builds the document records its
size, and `pushPack` reads the recorded number.

- `storeSnapshot` materializes the document every `SnapshotInterval` changes.
  `CreateSnapshotInfo` (both drivers) now stores the document's **live size**
  — `DocSize().Live.Total()`, data plus metadata, without garbage — as
  `live_size` on the snapshot row (`SnapshotInfo.LiveSize`).
- `checkDocumentSize` (`server/packs/size_gate.go`), called from `pushPack`
  under `DocPushKey` after the epoch and serverSeq checks, reads the latest
  snapshot's metadata with `FindClosestSnapshotInfo(..., false)`: one indexed
  read without the snapshot body, and only for packs that can grow the
  document.

The size lives on the snapshot row, not on `DocInfo`. An earlier attempt kept
it on `DocInfo` and was reverted: `CreateChangeInfos` caches the caller's
`DocInfo` by reference, so writing the field raced every cache hit's deep copy,
and invalidating the entry turned an unlocked read on the pull path into a
stale overwrite and `ErrConflictOnUpdate`. The snapshot row is written once
and never mutated, so it has no such contract. It also resets itself:
compaction purges a document's snapshots, so the size goes back to unknown
instead of going on refusing growth on a document compaction just shrank.

**Live, not Total.** `DocSize.Total()` is `Live + GC`, and deleting content
moves bytes from `Live` to `GC` without shrinking `Total`. A gate on `Total`
cannot see a document get smaller until garbage collection runs, which needs
every peer's version vector to move past the tombstones. `Live` drops as soon
as the deletion is measured. Since `Live <= Total`, the server's gate is also
never stricter than the SDK's own check against the same limit, so an honest
client's local check trips first.

**Live has to equal a rebuild.** The stored size is the running ledger of
the document the server built, so it must match what rebuilding the same
document reports (`DeepCopy().DocSize()`, a snapshot load), or the gate
refuses writes for bytes the document no longer holds. Purging a Tree
element detaches its whole subtree, and a descendant can still be live: an
insert or an identity restore concurrent with the element's removal leaves
one there, with no GC pair and charged to `Live`, while a rebuild walks
only the attached tree (yorkie#2244). The rule that keeps the two equal:

- **A detached node is a tombstone, and stays one.** When a collection
  purges a removed element, the purge tombstones every live node under it
  (`GCLiveRelease`, `Tree.purgeChild`) and `Root` moves their bytes from
  `Live` to `GC`, under a pair of each node's own that collect leaves alone
  and the drop of its subtree removes (see "How long a detached subtree is
  kept" below); it happens only with the purge, never without it. In a
  Tree element that is itself removed, the bytes come out of the element's
  GC charge instead, which its removal moved them to, and in one the same
  collection already deregistered, which took its charge with it, nothing
  is booked (`Root.bookRelease`, `TestReleaseInsideARemovedTreeElement`,
  `TestAReleaseInACollectedTreeBooksNothing`). Each takes
  the removal of its nearest removed ancestor, which depends on the subtree
  alone and not on which of two pairs a collection purged first in Go's map
  order (`TestReleaseTicketDoesNotDependOnPurgeOrder`). They leave
  the document with the element, as if its removal had covered them.
  Everything a purge detaches is therefore a tombstone off `Live`, and
  each is counted once, in `GC`, from its release to its drop: a piece
  split off a released node books its split overhead, as one split off a
  tombstone does, while its bytes stay under its source's pair, and a node
  a merge carries into a detached subtree is booked as the purge books one
  (`TestSplitOfAReleasedTextKeepsTheLedger`,
  `TestSplitOfAMergedInReleasedTextKeepsTheLedger`).
- Nothing makes a detached node live again. `Restore` resolves a span
  naming one as a replica that purged it does: the node leaves
  `NodeMapByID` and its chain and stays a tombstone, and the span is
  recreated only where its parent is attached (`Tree.forgetDetached`). The
  node's pending GC pair is dropped with it, with its bytes out of GC, so a
  recreate under the same ID registers its own
  (`TestRestoreAfterDetachKeepsTheLedger`). `recreateFromSpan`
  places nothing under a detached element, `Retombstone` skips one, and
  content placed under a detached parent, or split off a detached node, is
  detached itself and born tombstoned, booked to GC only.
- Detached nodes move from `NodeMapByID` to a side index,
  `Tree.detachedByID`, so `NodeMapByID` holds exactly the nodes reachable
  from the root. Lookups consult both and chains keep them, as on main, so
  every lookup and walk that reached them before still does; no such path
  can charge `Live` for one. One with a GC pair of its own leaves when that
  pair is collected, and every one leaves when a later collection lets its
  subtree go (see "How long a detached subtree is kept" below).
- A position inside a detached subtree resolves to the place the subtree
  was purged from (`Tree.purgedSlot`): the purge records the parent and the
  sibling it sat in front of. A replica that has not purged the subtree
  resolves the same position at that place too, as the subtree is out of
  sight there, and a live node under a removed ancestor resolves as from
  the topmost removed one. So a range with one end in a detached subtree
  styles, or deletes without merging, the same attached content on both
  (`TestEditWithOneEndInADetachedSubtree`,
  `TestStyleWithAnEndInADetachedSubtree`), and so does a range with its two
  ends in two purged subtrees, over the attached content between them
  (`TestRangeBetweenTwoDetachedSubtrees`). A delete that merges out of the
  subtree does not converge: a replica that has not purged it moves the
  hidden content after the range end into view, and one that purged it has
  nothing to move (`TestEditMergingOutOfADetachedSubtree`). Only a late or
  crafted op names a position inside a removed element, and main diverges
  the same way. The recorded place is runtime state, like the detached
  mark: a replica reloaded from a snapshot, and the server, which rebuilds
  from one, hold no detached node, so a late op naming one fails there
  while a running replica that purged it applies it; main splits the same
  way. And a sibling recorded at the purge that has since left the slot
  parent by a merge, not a purge, leaves the slot at the parent's end:
  whether a replica that has not purged the subtree moved it along depends
  on which side of the merging range the subtree sat, which the purged
  replica does not record. Like the cases above, only a late or crafted op
  names a position there.

**One undo, one result.** A replica that collected further holds no
detached node there, nor does one loaded from a snapshot. That is why
`Restore` resolves a detached node as if it were gone rather than leaving
it as it is: every replica then takes the same branch for the same span
(`TestUndoMatchesOnTheCloneWithoutDetachedNodes`). A `DeepCopy`, which is
how the document builds the clone a change runs on before the root, does
carry the detached subtrees, with their marks, purge slots and index
entries (`Tree.copyDetachedFrom`), so any op that resolves on the root
through a detached node or a purge slot resolves on the clone too, instead
of failing there and aborting the pack
(`TestRemoteOpNamingADetachedNodeAppliesOnTheClone`). Main's clone holds
none of its detached nodes, so main has that gap; this change closes it.

**Garbage a reload does not see.** `Root.DeepCopy` books, for the copy of
each tree node, the pairs the root itself holds for it, the detached nodes'
included (`Root.copyTreePairs`), each Tree paired with its copy by walking
the root's object as `NewRoot` walks the copy's, so a Tree the element map
holds but the object no longer reaches is carried by neither
(`TestDeepCopyCarriesNothingForATreeTheObjectNoLongerReaches`), and a
`DeepCopy` reports the GC and
`GarbageLen` its source does; booking them through `RegisterGCPair` would
let a detached node sharing an ID with an attached tombstone cancel that
one's pair (`TestDeepCopyBooksTheRootsOwnTreePairs`). A restore that
forgets a detached node drops its pair and those of its removed attributes
(`TestRestoreForgettingADetachedNodeDropsItsAttributePairs`). Because the
copy takes the root's pairs, it is no oracle for them: `Root.Rederive`,
which `assertRebuildsSame` uses, derives each Tree's pairs from the content
instead, NewRoot's scan of the attached tree and the pairs of the detached
nodes it carries, released ones included. A snapshot holds no detached
node, so a replica loaded from one has no pair for a detached tombstone
whose own removal is not yet collectable, for a removed attribute of a
detached node, or for a released node. Until the collection that purges
such a pair, or lets its subtree go, the running `GC` and `GarbageLen` hold
that garbage and a reload does not, the same pending-pair window main has
for the descendants of a purged node. `Live` is not affected: no
detached node is in it, so a reload reports the `Live` the running ledger
does (`TestReleaseLiveEqualsASnapshotReload`).

**How long a detached subtree is kept.** The detached state is there for
changes that name a node after a replica purged it, so it has to outlive
the purge, but not the document. Each collection lets go of every
detached subtree its vector has seen whole: every node in it created, and
merged in, at a ticket the vector covers (`Root.dropDetached`,
`Tree.dropDetached`). It does so before it purges, for subtrees an earlier
collection took out, and after, for those it took out itself, so one call
leaves nothing the next at the same vector would collect
(`TestLiveReleaseWaitsForTheBarrier`). A subtree is kept past its purge only
while a node in it is unseen, and that is the subtree a change can still
name a node in. A ticket the replica's own version vector does not know
counts as seen: every node a replica holds was created, and merged in, by a
change it applied, so such a ticket is forged, a phantom actor or a
lamport no change reached, and trusting it would keep the subtree, and its
bytes in GC, for good (`Root.GarbageCollectKnown`,
`TestAForgedTicketDoesNotKeepADetachedSubtree`,
`TestADocumentCollectsWithItsOwnVersionVector`). The nodes leave `detachedByID` and the merge stamp index,
the split links of attached nodes skip them, and the pairs they still hold,
their removed attributes' included, are dropped with their bytes out of GC
(`TestALaterCollectionDropsADetachedSubtree`,
`TestDroppingASubtreeUnlinksItsSplitSiblings`,
`TestDroppingASubtreeDropsItsAttributePairs`,
`TestDroppingABornDeadSplitPieceKeepsTheLedger`). A change naming one after
that fails to resolve, as on a replica that keeps no detached nodes, or on
main for a node whose own pair was collected.

The coverage is what keeps honest changes resolving. A purge at vector V
comes after every change concurrent with the removal it collects, but a
change that is not concurrent with the removal can still name a node a
concurrent split carried into the subtree, or a concurrent merge moved
there, as long as it has not seen that split or merge: it is concurrent
with the node's creation or merge, and the vector covers that ticket only
once every such change has been delivered. In merge seed 528 the deleting
replica, which never saw a split, merges across text the split carried into
a product the delete's cascade took, after the other replica purged the
product at a vector covering the delete and not the split. Dropping at the
next later vector failed that change with "node not found"
(`TestASubtreeASplitFedStaysForItsAuthor`). That subtree holds both nodes
the vector has not seen created and nodes it has not seen merged in, so
either check alone keeps 528 as main has it, a divergence and not an error;
each is pinned on its own, by a subtree only the other one would let go
(`TestADetachedSubtreeStaysUntilItsNodesAreSeen`). An earlier revision also
waited for a vector strictly after the purging one, for a late change
(yorkie#2110); over 19 families × 3000 seeds in each mode, lagging included,
no row moves without it, and it stalled whenever a vector was not
componentwise after the recorded one, so it is gone. The rule reads the
subtree and the vector alone, so the root and its clone, which collect at
the same vectors, let go alike.

**What is kept is counted.** A detached subtree is kept until a
collection's vector has seen every node in it created and merged in, so a
minimum vector that stops moving keeps it, and a client that holds its own
clock behind can keep feeding a subtree nodes with tickets the minimum has
not seen. That is the class main already has with a stalled minimum vector:
no garbage is collected under it at all, on main or here. What this change
adds is that the detached bytes are counted: a released node's are in `GC`
from its release to its drop, as a tombstone's are until its purge, so the
memory a replica holds for them shows in `docSize.GC` instead of hiding in
neither figure. Main kept these nodes too: a live node under a purged
element stayed in `NodeMapByID`, charged to `Live` for good (yorkie#2244),
and nothing ever freed it; here it leaves `Live` at the purge and the
document at the drop. A size cap that dropped subtrees before their
coverage was tried and rejected: replicas purge at different times, so it
would drop a subtree on one replica that another still holds, and an honest
change naming a node in it would then fail on the first and apply on the
second, which a client holding its clock back could force.

The drop reaches every node a purge released, and every collection that
takes one out of the document gives its bytes back: the tops of the
detached subtrees are recorded at the purge, so a node whose ID another
holds, which `detachedByID` does not index, is let go with the rest
(`TestDroppingReachesAReleasedNodeAnotherHoldsTheIDOf`); a released node
whose pair key another node's pair holds, which only crafted duplicate IDs
give, still has its bytes moved to GC, held aside for the drop to give back
(`TestARetainedBookingUnderATakenKeyIsKept`); and a Tree element the
collection deregisters drops its detached subtrees with their pairs, since
collect leaves a released node's pair to the drop
(`TestCollectingATreeDropsItsReleasedPairs`). A subtree the collection that
purges it has already seen whole goes in that call, so its bytes show in
`GC`, which the client's `MaxSizeLimit` reads with `Live`, only while the
subtree is kept for a change that can still name it; main kept these bytes
in `Live` for good. A late change (yorkie#2110) naming a node in a subtree
the purging collection saw whole then fails to resolve, as on main for a
purged node; over 19 families × 3000 seeds in each mode, lagging included,
no row moves for it. The detached tests keep their subtrees by a merge the
collection has not seen.

**Born-tombstoned content and the size gate.** Content an `Edit` inserts
under a removed parent is born a tombstone and booked to `GC` alone. Main
booked its pair as an ordinary one, which moved bytes from `Live` that had
never been added to it, so `Live`, the figure the server's
`MaxSizePerDocument` gate reads, fell below a rebuild by that content.
Booking it to `GC` alone leaves `Live` as a rebuild reports it: the change
moves the gate's figure toward a rebuild, never away from it.

| Path | With a detached node | Pinned by |
|---|---|---|
| GC purge of an ancestor | live nodes under it are released with it | `TestCollectReleasesALiveDescendantWithItsAncestor`, `TestLiveReleaseWaitsForTheBarrier`, `TestTreePurgeWithLiveDescendant`, `TestTreeCraftedReviveCannotPinARemovedChain` |
| GC purge inside a detached subtree | walks nothing; one pass is O(nodes) | `TestPurgeInsideADetachedSubtreeWalksNothing`, `TestCollectWalksADeletedSubtreeOnce` |
| `Restore`, element and text spans | resolved as purged: left a tombstone, recreated only under a parent that is there | `TestRestoreLeavesADetachedNodeATombstone`, `TestUndoMatchesOnTheCloneWithoutDetachedNodes` |
| `Restore` under an ancestor whose removal it knew | left a tombstone, attached or not | `TestTreeReviveUnderAKnownRemovalDoesNotDependOnGC`, `TestTreeRestoreUnderAKnownRemoval` |
| `recreateFromSpan` under a detached parent or split home | declined, as under a purged one | `TestRestoreRecreatesNothingUnderADetachedParent`, `TestRecreateGoesNowhereDetachedThroughASplitHome` |
| `Retombstone`, the §4.1 cascade, `refreshSpanAnchors` | skipped: no cascade, the span keeps its anchors | `TestRetombstoneLeavesADetachedNodeAlone` |
| `Edit` insert under a detached or removed parent | born tombstoned and detached, GC only | `TestEditUnderADetachedParentNamingAReleasedNode`, `TestTreeOpsNamingACollectedSubtree` |
| `Edit` delete over one | tombstones only; no ledger change | `TestTreeOpsNamingACollectedSubtree` |
| `Edit`, `Style`, `RemoveStyle` with an end in one | that end resolves to the place the subtree was purged from | `TestEditWithOneEndInADetachedSubtree`, `TestStyleWithAnEndInADetachedSubtree` |
| `Split` of a detached node | the product is detached too | `TestSplitsInADetachedSubtreeStayDetached` |
| §7.4 move of a split product across a purge | the product takes its new parent's side, released into a detached one | `TestSplitMovedAcrossTheDetachedBoundary` |
| Phase 6 merge into a detached destination | the moved nodes take its side, released, and nothing detached is moved into the attached tree | `TestMergeIntoADetachedParentTakesItsSide` |
| §7.4 move of an empty product cut off a node under a removed ancestor | born tombstoned before it moves, as on a replica that released the node | `TestSplitOutOfSightMovesOutATombstone` |
| `FindTreeNodesWithSplitText`, splits, chain walks | read tombstones as on main | `TestTreeOpsNamingACollectedSubtree` |
| `dissolveMergeSources`, `mergeStamps` | stamps of the subtree stay indexed, so a restore dissolves alike on a replica that purged and one that did not; a node's stamp leaves with the node | `TestDissolutionKeepsTheSourcePointerWhetherOrNotPurged`, `TestPurgeForgetsTheMergeStampsOfTheDetachedSubtree` |
| `Tree.GCPairs` (a rebuild) | not reached: walks the attached tree | `assertRebuildsSame` in the tests above |
| `Root.DeepCopy` | the copy holds the subtrees and the root's own pairs for them, in one pass over each pair map; a pair whose node left the tree is booked for a copy of the node; a tree of another shape keeps the scan's pairs | `TestDeepCopyBooksTheRootsOwnTreePairs`, `TestDeepCopyKeepsASlotThroughAnUnregisteredParent`, `TestDeepCopyBooksAPairWhoseNodeLeftTheTree`, `TestCopyDetachedFromATreeOfAnotherShape` |
| `Root.Rederive` (the ledger oracle) | derives the pending pairs of the detached nodes from the nodes, not from the root's map | `assertRebuildsSame`, `TestDeepCopyBooksADetachedTombstonesPendingPair` |
| A later collection | lets go of each subtree its vector has seen whole | `TestALaterCollectionDropsADetachedSubtree`, `TestADetachedSubtreeStaysUntilItsNodesAreSeen` |

**Why release instead of hold.** An earlier revision held a removed element
back from GC while a live node was under it, until that node was removed.
No one can see a node under a removed element, so no one could remove it:
an honest insert concurrent with the removal, or a crafted restore that
claims not to know it, kept the whole ancestor chain in `GC` for good, and
the server's size gate reads `Live`, not `GC`. Releasing ends with the
collection vector, which no client input can hold back: what a pass purges
is decided by `removedAt`, `PurgeBarrierAt` and the tree's shape, as on
main, and the release runs only once both have let the element go.

**Replicas release alike.** The release reads the tree at the moment each
replica collects, and replicas collect at different points of the change
log. A client's synced vector covers a removal only after it has pushed
every change it made before learning of it, and the server hands out a
minimum vector only with the changes behind it, so an honest change
concurrent with the removal is on every replica before any of them purges
the element, and they release the same nodes. The server race of
yorkie#2110 breaks that contract, since a minimum vector can arrive ahead
of a change behind it; the conformance suite's lagging mode models it.
Under the race, as for a crafted change, a change reaches a replica after
its purge and names a node that is gone or detached: `Restore` never
revives a detached node, and recreates one only where the span's parent is
attached, so it lands as the release left it everywhere else. Released
nodes stay registered, so an edit whose position names one, such as an
insert under a detached tombstone anchored on a released node, resolves
and applies as on main, with its content born tombstoned and detached
instead of charged to `Live`
(`TestEditUnderADetachedParentNamingAReleasedNode`). An edit or style
with an end in a detached subtree resolves that end at the place the
subtree was purged from, where main counted an index inside the detached
subtree and could delete or style unrelated attached content. An op naming a node the
purge unregistered fails, as on main. Under the race, the conformance
suite's lagging mode turns two runs into errors main did not report: uds3
2520, a late op naming a node the race let one replica purge first, and ud
11, which fails the same way in normal mode on main and on this change and
which main's lagging run only masked; both are inputs the protocol
contract excludes. `Restore` also leaves a
node a tombstone under an ancestor whose removal the restoring change knew
(`Tree.knownRemovalAbove`), which keeps the result before GC from depending
on it: such a revive would stay live only on replicas that had not
collected yet, and an undo of the ancestor's removal would then bring it
back on those alone.

Two divergences remain, both as on main. Whether a concurrent identity
revive or the removal wins depends on the order a replica applied them
(yorkie#2247). And content no undo span names, such as an insert concurrent
with the removal, comes back with the ancestor's undo only on replicas that
applied the undo before collecting; one that collected first recreates the
ancestor without it, as main does for anything a purge took.

**Unknown admits.** A zero `live_size` — no snapshot yet, a snapshot written
before the field existed, or a document just compacted — admits the push. The
first snapshot after that arms the gate.

### What refusing does to the client

By the time the server can refuse, the client has already applied the change
locally. Yorkie has no "your change was rejected, roll it back" message, and a
naive gate deadlocks the document: if every push is refused, the push that
would delete content is refused for the same reason the push that added it
was.

The decision is to **refuse growth only**:

- When the recorded live size is **strictly above** `MaxSizePerDocument`,
  `pushPack` refuses a pack if any of its changes **can grow** the document.
  Classification is by operation kind (`canGrow`), because the push path has
  no root to execute against, and is conservative: everything grows except
  - `Remove`;
  - `Edit` with no content, no attributes and no restore spans (a text
    deletion);
  - `TreeEdit` with no contents, no split and no restore spans (a tree
    deletion);
  - a change with no operations (presence only).
- The refusal is `document.ErrDocumentSizeExceedsLimit`, the error the SDK
  already raises for its local check, now carrying the code
  `ErrDocumentSizeExceedsLimit` (status `ResourceExhausted`). Nothing is
  stored and the server seq does not move.
- **Detach and remove always go through.** On an over-quota document, a
  detach or remove pack whose changes would grow it has those changes (the
  whole pack's) discarded and the status change proceeds, as stale-epoch
  changes are. Admitting them instead
  would let a client grow the document without bound by attaching, pushing
  inside the detach pack and attaching again.

A deletion can still add a little node metadata where it splits a text or tree
node at the range boundary. That growth is bounded by the content already in
the document, since each position can be deleted only once.

The only client this refuses is one that skipped its own check, or one whose
pack mixes deletions with growth: the whole pack is refused. Resending it at
once gets the same answer, so both SDKs' realtime sync loops stop retrying the
document every round and keep syncing the client's other documents; an
explicit sync still reports the refusal. The refused change stays queued, so
deleting content on that client afterwards does not help either: a pack
holding the change is refused whole.

The refusal is not permanent, though. The gate reads the project's limit and
the document's latest snapshot size, and both can move without the refused
client being party to it: an operator raises the limit, or peers delete
content. So the Go client holds the document off for `RejectedPushRetryDelay`
(10 seconds by default) and then re-probes, rather than waiting for an
explicit sync an app driven by the watch stream alone never makes. A document
waiting out a refusal pulls nothing either, since push and pull share one
`PushPull` and the refused pack goes with every pull, so that delay is also
how stale its view of its peers can get. The JS SDK can also drop the refused
change through its resync; the Go client cannot yet.

### The server-side write paths

Three writes do not come from a client and do not fit the lagging gate,
because each one builds the whole root on the server and therefore knows the
result exactly:

- `documents.CreateDocument` (admin/MCP) writes the initial root straight
  through `DB.CompactChangeInfos` and never reaches `pushPack` at all.
- `documents.UpdateDocument` (admin) and `revisions.RestoreRevision` rebuild
  the document and push it with `Status: attached`. Their packs are
  `SetYSON`-shaped, so `canGrow` says they grow, and the lagging gate would
  refuse them on exactly the over-quota documents they exist to repair —
  including a restore that brings the document back under the limit.

All three call `packs.CheckLiveSize` on the document they hold, which measures
`DocSize().Live` against `MaxSizePerDocument` and raises the same
`ErrDocumentSizeExceedsLimit`. The two push paths then set
`PushPullOptions.SizeChecked`, which skips the lagging gate for that push
only. `SizeChecked` is never set from a client request.

### Contract: the overshoot

The recorded size is the one the last snapshot measured, so the gate lags the
document by up to one `SnapshotInterval` of changes (default 500). Snapshots
are taken after pushes, in the background, and skipped while another one for
the same document is running. Each of those changes arrives in a push capped
by `maxRequestBytes` (16 MiB, `server/rpc/server.go`). So a document can
exceed `MaxSizePerDocument` by what one snapshot interval of changes adds
before the gate refuses further growth. That is part of the quota's contract;
projects that need a tighter bound lower `SnapshotInterval`.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Gate deadlocks a document that is already over quota | Refuse growth only, gate on `Live`, and always let detach and remove through |
| Lagging size lets a document exceed the quota by one snapshot interval | Stated as the quota's contract above; tighten `SnapshotInterval` for projects that care |
| A size source on `DocInfo` races the `docCache` | The size lives on the snapshot row, written once and read with its own query |
| Snapshots written before the field existed carry no size | Zero means unknown and admits; the next snapshot records it |
| Compaction shrinks a document while an old size keeps refusing growth | Compaction purges snapshots, so the size goes back to unknown |
| Server and client disagree on the number, so an honest client is refused | The server compares `Live`, which is at most the client's `Total`; making the accumulator agree with a rebuild is tracked by the rebuild-drift work |
| A pack mixing deletions with growth is refused whole and the SDK retries it | The SDKs stop resending it every round; the Go client re-probes once per `RejectedPushRetryDelay`, so a limit raised later is picked up; see above |
| One more read on the push path | Only for packs that can grow, and only the snapshot's metadata through an index |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Lagging gate over an exact one | An exact gate means building the document on every push; the whole point of the push path is that it is document-free |
| Size on the snapshot row, not on `DocInfo` | A field on the cached `DocInfo` inherits the `docCache` concurrency contract, which the reverted first attempt broke; the snapshot row is immutable and compaction already purges it |
| Live size, not Total | Deleting content shrinks `Live` at once; `Total` waits for garbage collection, which would hold a shrunk document over the quota |
| Refuse growth only | A blanket refusal deadlocks an over-quota document |
| Drop growth from detach and remove packs instead of admitting it | Admitting it would turn attach, detach-with-changes, attach into a way around the gate |
| Reuse `ErrDocumentSizeExceedsLimit` | It is the error the SDKs already know for the same quota |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Cap the bytes of a single change pack | Covered by `maxRequestBytes` (`server/rpc/server.go`), and it bounds one push, not a quota reached over many |
| Read the size off `be.Cache.Snapshot` in `pushPack` | Free, but never warm for a client that only pushes — `storeSnapshot` builds its own document and does not populate that cache |
| Persist the size on `DocInfo` | Tried and reverted; see above |
| Rebuild the document in `pushPack` | Correct and exact, but adds a snapshot load, a range query, a GC pass and two `DeepCopy` calls to every push |
| Refuse everything and define a recovery | Needs a terminal SDK state for every client, including honest ones mid-deletion |
| Do not refuse; detach or freeze out of band | Leaves the push path unbounded between housekeeping runs |
| Leave enforcement client-side | The quota is then advisory against anything but a stock SDK |

## Tasks

Implemented under
`docs/tasks/active/20261003-server-side-document-size-gate-todo.md`, which also
lists the SDK follow-up.

The accounting half — making the running `DocSize` accumulator agree with a
rebuild, so that the number the gate reads means the same thing on both sides —
is tracked separately.

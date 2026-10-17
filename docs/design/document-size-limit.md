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
  (`GCLiveRelease`, `Tree.PurgeReleasing`) and `Root` takes their bytes
  off `Live`; it happens only with the purge, never without it. They leave
  the document with the element, as if its removal had covered them.
  Everything a purge detaches is therefore a tombstone off `Live`.
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
  pair is collected; a released node has none and stays, as main's live
  detached nodes do.
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
  (`TestRangeBetweenTwoDetachedSubtrees`). A recorded sibling that has
  since left the slot parent by a merge, not a purge, leaves the slot at
  the parent's end; whether an unpurged replica moved the subtree along
  with it depends on which side of the merging range the subtree sat, which
  the purged replica cannot tell, so that case is the merge case below. A delete that merges out of the
  subtree does not converge: a replica that has not purged it moves the
  hidden content after the range end into view, and one that purged it has
  nothing to move (`TestEditMergingOutOfADetachedSubtree`). Only a late or
  crafted op names a position inside a removed element, and main diverges
  the same way. The recorded place is runtime state, like the detached
  mark: a replica reloaded from a snapshot, and the server, which rebuilds
  from one, hold no detached node, so a late op naming one fails there
  while a running replica that purged it applies it; main splits the same
  way.

**One undo, one result.** A copy built from the attached tree holds no
detached node: a replica that collected further, a snapshot, a `DeepCopy`,
and the document's clone, which an undo or redo runs on before the root.
That is why `Restore` resolves a detached node as if it were gone rather
than leaving it as it is: the clone and the root, and every replica, then
take the same branch for the same span
(`TestUndoMatchesOnTheCloneWithoutDetachedNodes`). An op other than
`Restore` that names a detached node can still resolve on the root and
fail on a rebuilt clone, which runs it first; main's rebuilt clone holds no
detached node either, so that gap is the same there.

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
| `FindTreeNodesWithSplitText`, splits, chain walks | read tombstones as on main | `TestTreeOpsNamingACollectedSubtree` |
| `dissolveMergeSources`, `mergeStamps` | stamps of the subtree leave the index at the purge | `TestPurgeForgetsTheMergeStampsOfTheDetachedSubtree` |
| `Tree.GCPairs` (a rebuild) | not reached: walks the attached tree | `assertRebuildsSame` in the tests above |

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

# Tree: survive two nodes sharing one TreeNodeID

**Created**: 2026-08-14

A document on wafflebase.io could not be opened: `AttachDocument`
returned `503 upstream connect error or disconnect/reset before headers.
reset reason: remote reset`, and the compaction housekeeping logged
`failed to compact document ...: unavailable: 503 Service Unavailable`
1278 times in 24 hours, all for that one document (wafflebase#725).

The serving pod was panicking:

```
http2: panic serving: runtime error: slice bounds out of range [:50] with capacity 49
  crdt.(*TreeNode).SplitText              tree.go:395
  crdt.(*Tree).FindTreeNodesWithSplitText tree.go:2303
  crdt.(*Tree).Edit                       tree.go:1397
  operations.(*TreeEdit).Execute
  document.(*InternalDocument).ApplyChanges
  packs.BuildInternalDocForServerSeq      snapshot.go:121
  packs.pullSnapshot ← preparePack ← pullPack ← PushPull
```

net/http resets the HTTP/2 stream when a handler panics, which the
gateway reports as `503 UR upstream_reset_before_response_started
{remote_reset}` — hence the client-visible 503 with no gRPC status.

## Problem

Replaying the document's stored snapshot and changes offline reproduced
the panic deterministically at one change, a plain range deletion:

```
TreeEdit from{left=34:4:ana7…:50} to{left=34:4:ana7…:51}
```

The tree held **two nodes under the id `34:4:ana7…:50`** — one live, one
tombstoned. They were created by an earlier change whose content node
carried an id from an older change:

```
seq=147  TreeEdit delete [34:4:…:50, 34:4:…:51)
seq=149  TreeEdit insert content={id=34:4:…:50 "R"}   ← the id it just deleted
```

That is the SDK's copy-reinsert undo path
(`tree_edit_operation.ts`, `cloneAndDropPreTombstoned`): it reverses a
deletion by re-inserting a deep copy of the removed nodes, and the copy
keeps the original id.

`NodeMapByID.Put` overwrites silently, so which of the two an id resolves
to depends on the order they were put:

| | put order | `Floor(34:4:…:50)` resolves to | `InsPrevID` |
|---|---|---|---|
| live replica | operation order | live copy | a node that spans offset 50 |
| rebuilt from snapshot | document order (`NewTree`) | tombstone | `34:4:…:0`, 49 units long |

`ToTreeNodes` follows `InsPrevID` when the position sits exactly at the
resolved node's offset, so on the rebuilt tree the position landed on a
node that ends before the offset it anchors, and `SplitText` sliced past
the end of its value.

Replaying the same history without the snapshot round trip applied every
change cleanly, which is why the document opened while its pod had it
cached and failed after a restart — "intermittent, self-healing" in the
issue.

## Constraints found while fixing

- Rejecting the offending change is not an option: it is already in the
  history of existing documents. An error there is a document that can
  never be loaded again — measured on the real data, the document fails
  at `seq=1578` instead of loading.
- Duplicate ids are **not** exclusively a symptom of the undo bug. The
  delimiters an element split consumes are simulated rather than
  replayed (the TODO in `operations.TreeEdit`), so two different nodes
  in one change can legitimately claim one id. `TestTree/edit its
  content with path when multi tree nodes passed` covers this, and a
  first version of the fix that treated every collision as corruption
  broke it. Content created by the edit's own change carries that
  change's lamport and actor, which separates the two cases.

## Tasks

- [x] Reproduce the panic offline from the stored snapshot and changes
- [x] Add regression tests for the invariant, the resolution divergence
      and the crash (`tree_duplicate_id_test.go`)
- [x] Guard `SplitText` against an out-of-range offset
      (`ErrSplitOutOfRange`)
- [x] Add `Tree.putNode`: keep the live node over a tombstone so a live
      replica and one rebuilt from a snapshot resolve an id the same way
- [x] Add `Tree.dropDuplicateContents`: drop content that reuses an id
      from an earlier change, keep content from this change
- [x] `go test ./...`, `make test` (integration), `make lint`
- [x] Verify against the production data: the document's snapshot and
      changes now replay cleanly, and replaying its full history creates
      no new duplicate ids
- [x] Make `Tree.Purge` remove only the entry the purged node holds.
      `NodeMapByID.Remove` is keyed by id, so collecting the tombstone of
      a duplicated pair unregistered the live node with it and put the
      document back where it started — found in review, reproduced, and
      now covered by `TestTreePurgeKeepsLiveNodeResolvable`
- [x] Cover the guard and both sides of the drop rule directly:
      `TestSplitTextRejectsOutOfRangeOffset`,
      `TestTreeEditKeepsCollidingContentFromSameChange`,
      `TestTreeEditDropsDuplicatedContentFromAnotherActor`
- [x] Write the three rules into `docs/design/tree.md`, since the SDKs
      have to apply them the same way

## Follow-ups

- The origin is client-side: the SDK should reverse deletions through
  the restore path (`restoreSpans`), which revives nodes under their
  original identity, instead of re-inserting copies. Until then an undo
  from those clients no longer restores the text.
- `putNode` and `dropDuplicateContents` need the same rules in
  `yorkie-js-sdk`, or a client and the server can resolve an id to
  different nodes.
- Colliding ids from the simulated split delimiters deserve their own
  fix: normal editing should not be able to produce two nodes under one
  identity.
- Documents that already carry duplicate ids in their snapshots keep
  them. They load, but nothing removes the duplicates.
- A dropped content node is silent. `pkg/document` has no logger — it is
  the shared document model, not server code — so counting drops needs
  the signal to travel out of `Tree.Edit` to a layer that can log it.
  Until then the divergence from a client that kept the content is not
  measurable in production.
- `ErrSplitOutOfRange` has no RPC mapping, so a position that still
  cannot resolve surfaces as an internal error. Worth mapping once
  something consumes it. More broadly, there is no panic-recovery
  interceptor: `recover()` appears nowhere outside tests, so the next
  panic will also reach clients as an unexplained 503.

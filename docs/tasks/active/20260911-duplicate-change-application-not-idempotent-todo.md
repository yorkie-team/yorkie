# Applying one change twice corrupts an object's createdAt index

**Created**: 2026-09-11

Found while fixing the undone-object-remove data loss
(`pkg/document/operations/set.go`) and confirmed **pre-existing on `main`**:
`git show v0.6.0:pkg/document/crdt/element_rht.go` has the same unconditional
write. Filed separately because it needs no undo/redo to reach, and because
containing it properly means deciding what idempotent operation application
should mean — a larger question than the release that surfaced it.

## Problem

`ElementRHT.SetWithExecutedAt` writes `nodeMapByCreatedAt` before it decides
the LWW race:

```go
node, ok := rht.nodeMapByKey[k]
newNode := newElementRHTNode(k, v)
rht.nodeMapByCreatedAt[v.CreatedAt().Key()] = newNode   // unconditional
```

When `v` loses, the key keeps its old occupant but the createdAt index now
points at the loser. Every other index in `Root` — `elementMap`,
`gcElementPairMap`, `sizeInGC` — is keyed by createdAt too, so from that
point on two distinct live elements answer to one identity and each index
can disagree with the next about which one it means.

The only way to give one createdAt two occupants is to apply the same `Set`
twice. Measured, with no undo anywhere: deliver the creating change to a peer
twice, then remove the member on the origin.

```
AFTER DUP    d1={"keep":0,"obj":{"k":1}} d2={"keep":0,"obj":{"k":1}}
AFTER REMOVE d1={"keep":0}               d2={"keep":0,"obj":{"k":1}}
AFTER GC     d1={"keep":0}               d2={"keep":0,"obj":{"k":1}}
```

`Remove` tombstones the loser that `nodeMapByCreatedAt` resolves to, while
`nodeMapByKey` — the index `Marshal`, `Get` and `Has` read — still holds the
live winner. The replicas diverge permanently.

## How it is reached

Duplicate application is defended at the server boundary, in two places, both
because operations are not idempotent:

- `pushPack` drops a pushed change whose `clientSeq` is at or below the
  client's stored checkpoint (`server/packs/pushpull.go:258-268`).
- `pullChangeInfos` drops a client's own changes on the way back out, with a
  comment naming the hazard: "non-idempotent ops double-count"
  (`pushpull.go:597`).

So the guards are what hold the line, not the CRDT. A test helper that pushes
without consuming the server's checkpoint reproduces it immediately, which is
how it was found.

**`pullSnapshot` is not behind either guard.** `pushPack` filters the list it
*stores* — `pushables` — and leaves `reqPack.Changes` untouched, and
`pullSnapshot` applies that raw list:

```go
doc, err := BuildInternalDocForServerSeq(ctx, be, docInfo, initialServerSeq)
...
if reqPack.HasChanges() {
    doc.ApplyChangePack(change.NewPack(..., reqPack.Changes, ...))
```

`initialSeq` is `docInfo.ServerSeq - len(pushables)` (`pushpull.go:327`), so on
a retry where every change was skipped as already pushed, it is the current
`serverSeq` and the document already holds them. `applyChanges` has no
version-vector deduplication, so they are applied a second time. The snapshot
built from that document is what the client receives and adopts.

Reaching it needs a client to retry a pack after a lost response *and* to be at
least `snapshotThreshold` changes behind, so the pull takes the snapshot branch
(`pushpull.go:472`). Rare, not impossible — and silent when it happens.

## Why there is no cheap containment fix

Every candidate guard sits downstream of the collision and cannot undo it:

- Skipping the byCA write for a loser breaks the ordinary concurrent-`Set`
  case, where the loser is tombstoned, registered as a GC pair, and must stay
  reachable by createdAt for `purge` to find it.
- Dropping the stale `gcElementPairMap` entry at collection time leaves
  `sizeInGC` — also keyed by createdAt — charging `docSize.GC` for an element
  that no longer owns the identity, so `DocSize()` diverges instead.

Once two elements share a createdAt, every createdAt-keyed structure is
ambiguous and there is no local repair. The fix has to prevent the collision.

## Tasks

- [ ] Decide whether operation application should be idempotent at the CRDT
      layer, or whether the server-boundary guards are the contract. If the
      latter, say so where `SetWithExecutedAt` can be read to promise
      otherwise, and consider rejecting a `Set` whose createdAt is already
      occupied rather than silently corrupting the index.
- [ ] Audit the other operations for the same shape: `Add` and `ArraySet`
      overwrite `RGATreeList.nodeMapByCreatedAt` and `elementMapByCreatedAt`
      in `insertAfter` with no collision check either.
- [ ] Close the `pullSnapshot` hole regardless of that decision: it should
      apply the changes `pushPack` accepted, not the ones the client sent.
      Cheapest shape is to hand the filtered list down rather than re-deriving
      it, so the two paths cannot drift apart again.
- [ ] Whatever the decision, pin it with a test that applies one change twice.

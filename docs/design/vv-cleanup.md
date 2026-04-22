---
title: vv-cleanup
target-version: 0.7.6
---

# Remove Detached Client's Lamport from Version Vectors

## Problem

Version Vector (VV) grows indefinitely as clients attach and detach from documents.
Each client's lamport entry remains in every other client's VV forever, bloating
network transfer (every Change carries a VV), storage, and minVV computation.

PR #1278 attempted to solve this but was closed because it lacked a clear definition
of a safe removal point — naively removing an actor from VV breaks causality
detection (cannot distinguish "detached actor whose ops are known" from "truly
unknown concurrent actor").

### Goals

- Remove detached client lamport entries from VV without losing causality information
- Define a safe removal point that all clients agree on
- Maintain correct GC and concurrent operation handling after removal
- Apply changes to both Go server and JS SDK

### Non-Goals

- Automatic cleanup of the client-side `detachedActors` map (deferred to Compaction)
- Optimizing minVV computation itself

## Design

### Safe Removal Condition

The server signals VV cleanup only when:

```
∀ attached client C_i, C_i.storedVV[detachedActor] >= detachedLamport
```

This guarantees that every in-flight change whose VV lacks the detached actor was
created by a client that already knew about the detach. Proof: Yorkie delivers
changes in server sequence order. If a client knew about the detach (received the
detach change), any change it creates afterwards has a higher server sequence than the
detach change. All other clients process changes in server sequence order, so they
process the detach before any post-detach change from the creator.

### VV Usage in the Codebase

VV serves two purposes:

| Purpose | Where | What VV is used |
|---------|-------|-----------------|
| GC condition | `crdt/root.go` `EqualToOrAfter` | minVersionVector (server-computed) |
| Causality detection | `crdt/tree.go`, `crdt/text.go`, `crdt/rga_tree_split.go` | Change ID's VV (per-operation) |

Both need to handle the case where a detached actor's entry has been removed.

### Server-Side Flow

```
Client detaches
  │
  ▼
Server stores DetachedLamport in ClientDocInfo (existing clients collection)
  │
  ▼
On each PushPull, server computes minVV and checks:
  minVV[actorID] >= detachedLamport ?
  │
  ├─ No  → do nothing (not all clients have caught up)
  │
  └─ Yes → include actorID in response's detached_actors field
            Reset DetachedLamport to 0 after all clients notified
```

### Client-Side Flow

```
Receive detached_actors in ChangePack response
  │
  ▼
For each detached actor:
  1. Add to document's detachedActors map (actorID → lamport)
  2. Remove from document's VV (Unset)
  │
  ▼
When executing remote changes:
  If changeVV.Get(actorID) == false:
    Check detachedActors[actorID]
    If found → treat as "known" (augment change VV before execution)
    If not found → treat as "unknown" (existing behavior)
  │
  ▼
When running GC:
  Augment minVV with detachedActors before calling GarbageCollect
```

### Why Augmenting Change VV Works

When the server signals removal, ALL clients have processed the detach. Any new
change without the actor in its VV was created by a client that knew about the
detach. The receiver also knows about the detach (it received the signal). So
augmenting the change's VV with the detached actor's lamport produces the same
causality result as if the actor were still in the VV.

For changes created before the detach (by offline clients etc.), the actor is still
in their VV because the server only signals removal after all clients have caught up.

### Proto Changes

```protobuf
message ChangePack {
  ...
  VersionVector version_vector = 7;
  // detached_actors contains actors safe to remove from VV.
  // Server signals this when all clients have caught up.
  map<string, int64> detached_actors = 8;  // NEW
}

message Snapshot {
  JSONElement root = 1;
  map<string, Presence> presences = 2;
  // detached_actors preserves removed actor lamports for VV
  // augmentation when restoring from snapshot.
  map<string, int64> detached_actors = 3;  // NEW
}
```

Both fields use the same name and data shape (`actorID → lamport`). The role differs
by context: ChangePack carries the server's removal signal, Snapshot preserves the
state for restoration. Both are additive — old clients ignore them.

### Database Schema

No new collection. Reuse the existing `clients` collection by adding a field to
`ClientDocInfo`:

```go
type ClientDocInfo struct {
    Status          string
    ServerSeq       int64
    ClientSeq       uint32
    Epoch           int64
    DetachedLamport int64  // NEW: lamport at detach time, 0 when not applicable
}
```

When a client detaches, `DetachDocument()` stores the lamport value. The server
queries detached clients by checking `DetachedLamport > 0` on the existing
`clients` collection. After all clients have been notified, `DetachedLamport` is
reset to 0.

This avoids a new collection, new indexes, and migration — the `clients` collection
already tracks per-document client state and has a `project_id` index.

### Implementation Scope

#### Go PR: Server + Go Client

| Layer | Changes |
|-------|---------|
| Proto | Add `detached_actors` to `ChangePack` (field 8) and `Snapshot` (field 3) |
| DB | Add `DetachedLamport` to `ClientDocInfo`, update `DetachDocument()` signature |
| MongoDB | Update `UpdateClientInfoAfterPushPull()` to store lamport on detach |
| Memory DB | Same changes for in-memory implementation |
| Server | In `pullPack()`: query detached clients, check minVV condition, populate `detached_actors` |
| Snapshot | Include `detached_actors` in snapshot creation |
| Go Client | Add `detachedActors` map to Document, augment change VV + minVV |
| Converter | Serialize/deserialize `detached_actors` in ChangePack and Snapshot |
| Tests | Integration tests for full detach → signal → cleanup cycle |

#### JS SDK PR: Client (after Go PR merges)

| Layer | Changes |
|-------|---------|
| Converter | `fromChangePack`: parse `detached_actors`, `fromSnapshot`: parse `detached_actors` |
| Document | Add `detachedActors: Map<string, bigint>` field |
| applyChangePack | Store detached actors, unset from VV |
| applyChange | Augment change VV with detachedActors before execution |
| garbageCollect | Augment minVV with detachedActors before GC |
| applySnapshot | Restore `detachedActors` from snapshot |
| Tests | Unit tests for augmentation logic, integration with server |

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Augmenting Change VV in-place mutates the object | Change objects are consumed once during execution, not reused |
| Snapshot format change | Proto field 3 is additive; old snapshots deserialize with empty map |
| Client re-attaches with same ActorID | Not possible; each ActivateClient generates a new ID |
| Long-offline client delays cleanup | Same limitation as existing minVV — unavoidable without timeout |
| DB query cost on PushPull | Same collection as existing client queries; DetachedLamport > 0 filter is lightweight; records reset after notification so no accumulation |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Signal via ChangePack field, not separate RPC | Piggybacks on existing PushPull flow; no extra round trip |
| Augment VV before Execute, not change ticketKnown signatures | Minimizes code changes; VV augmentation is a single point |
| Store detachedActors on document, not in VV type | Keeps VV type simple (`map[ActorID]int64`); avoids proto/serialization changes to VV |
| Wait for ALL clients before signaling removal | Prevents the offline-client bug where a change creator genuinely didn't know the actor |
| Reuse `ClientDocInfo` instead of new collection | ClientDocInfo already tracks per-document client state; avoids new collection, index, and migration overhead |
| Unified `detached_actors` name in proto | ChangePack and Snapshot carry same data shape; role difference explained by comments |
| `DetachedLamport` field name (not just `Lamport`) | Explicit about when the value was captured; avoids ambiguity when client is in attached state |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| New `detachedclients` collection | Duplicates data already in `clients` collection; unnecessary migration |
| Keep detached VV records as tombstones in `versionvectors` | Requires changing deletion logic in `updateVersionVector` and query filters in `GetMinVersionVector`; high impact |
| Remove from server-side minVV only, keep in client VV | Doesn't reduce VV size in changes (main bandwidth concern) |
| DAG-based causality tracking | Correct but requires major architectural change |
| Epoch-based floor (single int64 replacing all removed actors) | Cannot distinguish detached actors from truly unknown new actors with lower lamport |
| Change VV type to struct with active + detached maps | Large structural change to VV type; affects proto, serialization, all consumers |
| Remove immediately on detach without waiting | Breaks causality for offline clients who haven't received the detach yet |
| Separate proto field names (`removable_actors` / `detached_actors`) | Same data shape; unified name is simpler, role difference handled by comments |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

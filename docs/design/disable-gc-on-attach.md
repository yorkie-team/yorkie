---
title: disable-gc-on-attach
target-version: 0.7.10
---

# Disable GC on Attach

## Problem

`ChangePack.VersionVector` (VV) is included in every PushPull response so the
client can run tombstone GC against the server-computed `minVV`. The VV size
grows with the number of unique actors that have ever written to the document,
not with the CRDT types in use.

For documents that only use commutative CRDTs (Counter, primitive value
replacement), the client never produces tombstones, so the response VV serves
no purpose on the receiving end. Three costs are paid for nothing:

1. **Response payload** — every PushPull response carries a VV of
   `O(num_actors)` entries.
2. **Server compute** — `UpdateMinVersionVector` is called on every PushPull;
   it reads every attached client's stored VV and recomputes the min. For a
   high-fan-out Counter document (live polls, telemetry, voting), this is
   `O(num_clients * num_actors)` work per push, with the result discarded.
3. **Server storage** — every client's VV is persisted in the
   `versionvectors` collection even when no client consumes it.

A use case driving this is high-fan-out Counter documents where dozens or
hundreds of clients are attached at once and none of them need tombstone GC.

### Goals

- Let a client opt out of receiving the response VV and participating in
  server-side `minVV` tracking, on a per-attachment basis.
- Eliminate the server-side `UpdateMinVersionVector` cost contributed by
  opted-out clients.
- Stay wire-compatible: existing clients keep the current behavior.

### Non-Goals

- Automatic detection of "GC-free" documents. A later schema-based mechanism
  (see [Schema Validation](schema-validation.md)) can drive the same flag, but
  static detection is out of scope here.
- Removing VV from `Change` IDs. Per-operation VV is required for causality
  detection in Tree/Text/Array and is not affected.
- Project-level toggle in the Dashboard. The flag lives on the attach call so
  documents within the same project can mix opt-out and opt-in clients.

## Design

### Contract

A client attaches with `disableGC = true` to declare:

> "I will not produce or consume tombstones on this document. I do not need
> the response VV, and the server does not need to track my VV for any other
> client's GC."

The server honors this declaration. It is the client's responsibility to keep
the promise; using Tree/Text/Array deletions on a `disableGC` attachment leads
to undefined GC behavior on that client (tombstones may not be reclaimed) and
may cause other clients to GC tombstones this client has not yet seen.

Counter, presence updates, and primitive (`LWW`) value replacement are safe.

### SDK API

```ts
// JS SDK
await client.attach(doc, { disableGC: true });
```

```go
// Go SDK
err := client.Attach(ctx, doc, client.WithDisableGC())
```

Default is `false`. The naming mirrors the existing server-side
`SnapshotDisableGC` configuration, so the intent ("this attachment does not
participate in GC") is consistent across the codebase.

### Wire Protocol

Add the flag to the two RPCs that drive a PushPull on behalf of an end
client:

```protobuf
message AttachDocumentRequest {
  string client_id = 1;
  ChangePack change_pack = 2;
  string schema_key = 3;
  bool disable_gc = 4;  // NEW
}

message PushPullChangesRequest {
  string client_id = 1;
  string document_id = 2;
  ChangePack change_pack = 3;
  bool push_only = 4;
  bool disable_gc = 5;  // NEW
}
```

`DetachDocument` and `RemoveDocument` deliberately do not carry the flag.
Both call PushPull at most once per session and a single extra minVV
write at terminus is negligible; carrying the flag there would force
clients to remember and resend it on a path that no longer matters.

The fields are additive; older servers ignore them, and older clients
never set them.

### Server Behavior

The flag flows through `PushPullOptions` from each RPC handler into
`server/packs/pushpull.go`:

```go
// in yorkie_server.AttachDocument / yorkie_server.PushPullChanges
pulled, err := packs.PushPull(ctx, s.backend, project, clientInfo, docKey, pack, packs.PushPullOptions{
    Mode:      ...,
    Status:    ...,
    DisableGC: req.Msg.DisableGc,
})

// in packs.pullPack
if opts.DisableGC {
    resPack.VersionVector = nil
} else {
    minVersionVector, err := be.DB.UpdateMinVersionVector(
        ctx, clientInfo, docInfo.RefKey(), reqPack.VersionVector,
    )
    if err != nil {
        return nil, err
    }
    if resPack.SnapshotLen() == 0 {
        resPack.VersionVector = minVersionVector
    }
}
```

The flag is **not persisted** on `ClientDocInfo` or anywhere else on the
server. Each PushPull reads it from the request alone. The cost is one
bool per request; the savings are no schema field, no migration, no
cache invalidation logic, and no risk of persisted state drifting out
of sync with client intent.

Two effects:

1. **No write to `versionvectors`** for this client. The server never stores
   the opt-out client's VV, so it does not appear in any future
   `GetMinVersionVector` query.
2. **`minVV` is computed over participating clients only.** Non-opted-out
   clients see a `minVV` that ignores opt-out attachments, which is correct
   because opted-out clients will not consume tombstones — there is no need
   to keep tombstones alive for them.

`GetMinVersionVector` already returns the min over the rows present in
`versionvectors`. Because we never insert rows for opted-out clients, the
existing query is correct without modification.

The request-side `reqPack.VersionVector` from an opt-out client may be empty;
the server does not read it for any other purpose, so this is safe.

### Client Behavior

Opted-out client (this iteration):

- Sends `disable_gc = true` in `AttachDocumentRequest`.
- Continues to maintain its own per-change VV for causality of operations it
  produces — `disableGC` controls GC participation, not change construction.
  For Counter-only workloads this VV stays trivial.
- Receives an empty `VersionVector` in the response. Calling
  `GarbageCollect(emptyVV)` is a safe no-op (no key meets the threshold),
  so no client-side change is required for correctness in this iteration.

Deferred client-side optimizations (future work):

- Skip serializing the request-side VV (`reqPack.VersionVector`) when the
  attachment is opt-out. The server already does not read it for opt-out
  clients, so the only cost today is wire bandwidth proportional to actor
  count.
- Skip the local `GarbageCollect` traversal entirely when the response VV
  is empty. The current no-op behavior is correct but walks the tombstone
  list once per pull.

Opt-in (default) client behavior is unchanged.

### Snapshot GC Edge Case

`server/packs/pushpull.go` calls `GetMinVersionVector` inside `pullSnapshot`
to GC tombstones from the snapshot it builds:

```go
if !be.Config.SnapshotDisableGC {
    vector, err := be.DB.GetMinVersionVector(ctx, docInfo.RefKey(), doc.VersionVector())
    if err != nil {
        return nil, err
    }
    if _, err := doc.GarbageCollect(vector); err != nil {
        return nil, err
    }
}
```

When every attached client is opted out, `versionvectors` has no rows for the
document. `MinVersionVector` is then called with only the caller-supplied
`doc.VersionVector()`. Because every key it contains is also the key with the
single minimum, the result equals `doc.VersionVector()` unchanged. Snapshot
GC therefore runs against the document's current max VV — the aggressive
case — and collects all reclaimable tombstones in the snapshot. This falls
out of the existing `MinVersionVector` semantics; no extra code is needed.

If at least one opt-in client is attached and has pushed a VV row, that
row's lamports anchor the min, restoring the normal (potentially
conservative) GC behavior for clients that need to see tombstones.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Client enables `disableGC` then performs tombstone-producing operations (Tree.Edit, Text.Edit, Array remove) | SDK documents the contract clearly. Misuse degrades that client's GC only; other clients' GC remains correct as long as they keep `disableGC = false`. |
| Mixed attach states on the same document | Supported by design. Min VV is computed over participating clients; opt-out clients neither contribute to nor consume the min. Tombstone GC proceeds at the pace of the slowest opt-in client. |
| Operator forgets the contract during code review | Schema-driven auto-application (future work, see Non-Goals) closes this. For now, the flag is per-attach and explicit in code. |
| Backward compatibility | New proto fields are additive; older clients never set them and older servers ignore them. |
| Client changes intent across requests | The flag is per-request. If a client sends opt-out one call and opt-in the next, each call honors the value it carried. Since the SDK derives the flag from the attachment, this is not a concern for the official client; third-party clients control their own behavior. |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Per-attachment flag, not per-project | Project is a workspace boundary in Yorkie, not a schema boundary. Documents within one project can have different CRDT usage profiles. |
| Per-request wire field, no server persistence | The flag travels on `AttachDocumentRequest` and `PushPullChangesRequest` and is read by the handler into `PushPullOptions`. The server does not persist it on `ClientDocInfo`. Persistence would add a BSON field (forever, in production data), test churn through the `AttachDocument` signature change, DeepCopy and cache-coherence updates — all to save one bool on the wire. Per-request is genuinely smaller end-to-end. |
| Skip `UpdateMinVersionVector` entirely for opt-out PushPulls | The min over participating clients is what opt-in clients need; opt-out clients should not gate GC for others. Skipping is correct and is the main compute win. |
| Field name `DisableGC` | Matches existing `SnapshotDisableGC` server option; describes the user-visible behavior (no GC) rather than the wire-level optimization (no VV in response). |
| Flag is on `AttachDocumentRequest` + `PushPullChangesRequest` only | These are the two RPCs that drive a PushPull on behalf of an end client. `DetachDocument` and `RemoveDocument` call PushPull at most once per session; carrying the flag there would burden every client to track a small one-time cost. |
| Snapshot GC uses `MinVersionVector` unchanged | When all clients are opt-out, the function returns the caller's `doc.VersionVector()` and the snapshot is GC'd aggressively. When at least one opt-in client has a stored row, that row anchors the min. Both regimes fall out of existing semantics with no extra code. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Project-level Dashboard toggle | Coarse-grained. Any non-Counter document in the project would be at risk. Project is not a schema boundary. |
| Server auto-detects "Counter-only" document | Requires runtime tracking of CRDT type usage per document, with re-evaluation on every operation. High complexity; race-prone when types are added. Better as a future schema-driven optimization. |
| Persist `disable_gc` on `ClientDocInfo` | Conceptually clean — set once, read everywhere — but ends up touching the schema, the `AttachDocument` signature, ~30 test call sites, DeepCopy, the memory and mongo `UpdateClientInfoAfterPushPull` paths, and the mongo client cache equality check. The per-request alternative carries 1 bool on the wire and touches none of that. |
| Per-request flag in `ChangePack` | The `ChangePack` is a payload object shared by many flows; sticking the wire-level flag in a top-level request field keeps responsibilities clean. |
| Send VV only when document has tombstones | Requires the server to know the tombstone state of every document, which adds bookkeeping for a narrower benefit than the per-attach opt-out. |
| Lazy/diff VV transmission | Cross-cutting wire change. Helps all docs marginally but does not eliminate `UpdateMinVersionVector` cost for Counter docs. Can be pursued independently. |
| Drop response VV but keep `UpdateMinVersionVector` | Saves only response bandwidth. Misses the larger server-side compute and storage win for high-fan-out Counter docs. |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

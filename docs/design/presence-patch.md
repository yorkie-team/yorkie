---
title: presence-patch
target-version: 0.7.25
---

# Presence Patch

## Problem

`presence.set({ cursor })` merges locally and sends the whole presence every
time: `PresenceChange{ type: PUT, presence: <full map> }`. Presence is a flat
map of top-level keys, so a client that keeps a large value in it, such as a
few hundred selected element ids, resends that value with every cursor move.
In one app this was about 7.4 KB per cursor update instead of about 250 B
(#2154).

`PUT` fits the presence path because the path is lossy by design.
Presence-only changes live only in an in-memory cache that can be evicted or
lost on restart, and a client that falls far behind gets a snapshot instead of
changes. With a full `PUT`, the next update heals whatever was missed. A patch
scheme has to keep that property.

### Goals

- Let a client send only the top-level presence keys that changed.
- Keep every pulled presence change a full `PUT`, so old SDKs and clients that
  missed an update see no difference.
- Never store or fan out a presence the sender does not hold.

### Non-Goals

- A patched downstream. Forwarding patches to new SDKs needs per-actor
  sequence numbers, gap detection and per-version fan-out.
- Patching inside a value. A patch is per top-level key; a large value that
  changes is still sent in full.
- The SDK side. This document fixes what the server accepts; the SDKs adopt it
  separately.

## Design

Upstream patch, downstream put: the server folds each patch into a full `PUT`
of the sender's presence before the change is stored.

### Wire

`PresenceChange.ChangeType` gains `CHANGE_TYPE_PATCH = 4`. A patch carries the
keys it sets in `presence` and the keys it deletes in a new
`repeated string removed_keys = 3`. A patch that only deletes may leave
`presence` unset.

The server advertises `presence-patch` in `ChangePack.capabilities` on every
pack it returns, attach and push-pull alike. A client sends a patch only while
the last pack it received carried the capability.

An older server does not reject a patch. Its converter has no case for the
type, so it decodes the change as an empty presence change and stores a nil
presence for the sender. During a rolling upgrade, a client that learned the
capability from an upgraded node can have one push land on an old one. The
reply from that node carries no capabilities, so the SDK must then stop
patching and send its full presence as a `PUT` right away. That heals the
presence in one round trip. From this version on, the converter refuses a
presence change type it does not define with
`ErrUnsupportedPresenceChangeType`, so a later type cannot hit the same trap.

### Fold

`packs.foldPresencePatches` runs in `PushPull` after the presence strip for
presence-disabled documents and before `pushPack`. It walks the changes
`pushPack` will store (clientSeq above the checkpoint) in clientSeq order,
tracking the sender's full presence:

| Change | Effect on the tracked presence |
|--------|--------------------------------|
| `PUT` | becomes the tracked presence |
| `CLEAR` | tracked presence becomes unknown |
| `PATCH` | applied to the tracked presence; the change is rewritten to a `PUT` of the result |
| no presence | none |

A patch applies its deletions first and then its sets, so a key in both ends
up set.

The walk starts from a per-server cache, `Cache.PresenceBase`, keyed by
document and client ID. An entry holds the client's full presence and the
checkpoint (server seq and client seq) and epoch the client had in the
document when the entry was written. The entry is used only when both still
match the client's current values.

Every push-pull rewrites the client's checkpoint, so an entry stays valid only
while every request of this client for this document went through this
server. Client seq alone is not enough: a detach resets it, and a re-attach
through another server can bring it back to the cached value. The server seq
cannot come back, because any stored change, including the detach's clear or
the re-attach's put, moves the document past it.

After the pull, `commit` walks the changes `pushPack` actually stored and
writes the resulting presence back under the client's new checkpoint. Changes
`pushPack` discarded (a stale epoch, or the size gate on a detach) are not in
that list, so the base stays what the server holds. A pull without changes
moves the entry to the new checkpoint without copying the presence. A push
that fails before the pull leaves the entry untouched, so its retry is folded
again from the same base.

A patch that has no base but is followed in the same pack by a `PUT` or
`CLEAR` loses its presence instead of failing the push: the later change
replaces the presence either way. A detach pack ends with a `CLEAR`, so this
keeps a detach after an eviction or a stale epoch from failing on a patch
whose result is thrown away.

### No base

A patch with no tracked presence (no entry, a stale entry, or a patch after a
`CLEAR`) fails the push with `ErrPresenceBaseUnavailable`
(`FailedPrecondition`). Nothing in the pack is stored. The client resends the
pack with that patch replaced by a full `PUT`.

The whole push is refused rather than just its presence. The server cannot
make up the presence, and storing the operations while dropping the presence
would leave the sender's peers showing a stale presence until the next `PUT`.
Retrying the operations costs only latency, since none were stored.

The SDK should send a full `PUT` on attach and after reconnecting, so this
path is rare: it is taken after an eviction, a restart or a shard move. It is
an expected signal, so it is not counted in the push-pull error metric.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A stale cached base produces a presence the client does not hold | The base is used only while the client's checkpoint and epoch are exactly the ones it was cached at |
| An evicted or lost base | `ErrPresenceBaseUnavailable`; the client falls back to a full `PUT` |
| A new SDK sends a patch to an old server during a rolling upgrade | The old server stores an empty presence; its reply has no capability, and the SDK resends a full `PUT` at once |
| Cache memory | LRU bounded by `--presence-base-cache-size` (default 10,000); an eviction costs one refused push |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Fold on the server, pull only `PUT` | Keeps the self-healing property and needs nothing from old SDKs |
| Key the base by client ID, not actor | Two sessions of one client key share a stable actor but have separate checkpoints; keying by actor would make them invalidate each other's base |
| A dedicated cache instead of reading the presence change store | The presence store is indexed by server seq per document; finding one client's latest presence would mean a scan, and the store can be evicted independently anyway |
| Advertise on `ChangePack.capabilities` | It is the existing negotiation channel and is returned on push-pull as well as attach, so a client learns about an upgraded server without re-attaching |
| Refuse the whole push when there is no base | See [No base](#no-base) |

`api.ServerCapabilities` is emitted for the first time with this change, so it
lists only `presence-patch`. It used to list `element-restore`, which the
converter does not decode yet; advertising it would let a client's restore be
stored as an ordinary insert.

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Forward patches downstream | Needs per-actor sequence numbers, gap detection and per-version fan-out; out of scope |
| Advertise in a new `AttachDocumentResponse` field | Duplicates the existing `ChangePack.capabilities` channel and is not seen on push-pull |
| Fold from the document's presence map in the snapshot | The snapshot lags behind presence-only changes and rebuilding it per push is expensive |
| Refuse only the presence part of a push | Leaves peers with a stale presence and changes what the error means to the SDK for little gain |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

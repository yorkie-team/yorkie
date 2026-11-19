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

An older server (v0.7.24 or earlier) does not reject a patch, and the result
is not harmless. Its converter has no case for the type, so it decodes the
change as a presence change with an empty change type and stores it. When it
serves the change back, its encoder maps that empty type to
`CHANGE_TYPE_UNSPECIFIED`, and every JS SDK that pulls it, older readers as
well as this version, throws `ErrInvalidType` (`unsupported type: 0`) and its
sync loop stops. Servers must therefore be upgraded before clients, and a
server must not be rolled back below this version once clients that send
patches are connected.

During a rolling upgrade, a client that learned the capability from an
upgraded node can still have one push land on an old one. The reply from that
node carries no capabilities, so the SDK must then stop patching and send its
full presence as a `PUT` right away. That stops further patches but does not
repair the one the old node already stored. From this version on, the converter refuses a
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
| `PATCH` | applied to the tracked presence; the change is rewritten to a `PUT` of the result, unless the result would pass `maxPresenceBaseSize`, which is handled as having no tracked presence, or the pack's folds have passed `maxPresenceFoldPerPack`, which keeps one `PUT` for the run (see [Fold budget](#fold-budget)) |
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

A detach on this server is the one transition the checkpoint does not record:
`ClientInfo.DetachDocument` zeroes both seqs to the (0, 0) a fresh attach
seeds, so an entry kept past a detach would match the next attachment. Two
rules close that: `commit` drops the entry when the client is no longer
attached to the document, and neither stores nor uses one at the initial
checkpoint, which identifies no attachment.

After the pull, `commit` walks the changes `pushPack` actually stored and
writes the resulting presence back under the client's new checkpoint, as the
store holds it. That is not always the reply's checkpoint: the store merges a
checkpoint with `$max`, so it never moves back, while a push-only reply returns
the server seq the client sent. A client that leaves a pull unapplied (the JS
SDK does during IME composition) sends a server seq behind the one that pull
recorded, and a base keyed to the reply would then never match the checkpoint
the next push reads. So `commit` keys the base to the client as the store holds
it, read back only when there is a base to keep; if that read fails, it drops
the base rather than key it to a checkpoint the store may not hold.

The read-back row is only trusted to describe this request while its client
seq still matches the one this push produced. The pull lock is per-process, so
a concurrent push by the same client on another server can advance the stored
client seq between the pull and the read-back, and that push carries presence
this request never saw. Keying this request's older presence to the newer
checkpoint would let the client's next patch fold onto presence the store has
already moved past, so a client seq that moved drops the base instead and the
client falls back to a full put. The server seq is not compared: it can
legitimately move under a concurrent pull, and an entry keyed behind it simply
misses, which costs a full put rather than correctness. Changes
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
`CLEAR`), or one whose result would pass `maxPresenceBaseSize`, fails the push
with `ErrPresenceBaseUnavailable`
(`FailedPrecondition`). Nothing in the pack is stored. The client resends the
pack with that patch replaced by a full `PUT`.

The whole push is refused rather than just its presence. The server cannot
make up the presence, and storing the operations while dropping the presence
would leave the sender's peers showing a stale presence until the next `PUT`.
Retrying the operations costs only latency, since none were stored.

The SDK should send a full `PUT` on attach and after reconnecting, so this
path is rare: it is taken after an eviction, a restart, a shard move, or a
re-attach. A client whose presence is over `maxPresenceBaseSize`, or whose
patch would take it over, takes it on every patch, since such a presence is
neither folded into nor cached; sending full `PUT`s is both the fallback and
the steady state for such a client. It is an expected signal, so it is not
counted in the push-pull error metric.

### Fold budget

`maxPresenceBaseSize` bounds one folded put, and nothing bounds how many
changes a pack carries, so that bound alone leaves a pack of minimal patches
— each needing only clientSeq continuity — rewritten into one full presence
per change, every one of them stored and fanned out.
`maxPresenceFoldPerPack` (64 KiB, 16 × the per-put bound) bounds the sum of
the puts a single pack's folds produce.

Past the budget the fold keeps folding, but stops writing a put per patch:
the patches of the run lose their presence and the last of them carries one
`PUT` of what the run produced. So a pack costs the budget plus one put
however many patches it carries, the sender's peers still end on the presence
it holds, and the intermediate presences dropped are the transient ones the
presence path is lossy about anyway. A `PUT` or `CLEAR` the client sent ends
a run — it replaces the presence, so the run needs no put of its own — and
the patches after it start a new one, whose put is bounded by the presence
that `PUT` carried and so is paid for in the client's own bytes.

The push is not refused. A missing base is a property of the server's state
that the client clears by sending the presence it holds as a `PUT`; passing
the budget is a property of the pack's shape, and the client is told no more
than that the base was unavailable, so a resend that replaced only the patch
the server stopped on would be refused again — operations included — for as
many round trips as the pack has patches. A client that pushes each presence
change as it happens never reaches the budget at all; one that batches
hundreds of them into a single pack does.

### Put size

Both bounds above cover only presence the server itself produces: the entry
it caches and the puts a fold writes. A `PUT` the client sent is exempt from
both, since the client paid for it in the bytes it sent. That pays for the
write, not for the fan-out: the put is stored as sent and then pulled by every
client attached to the document, and presence is in no document size gate
(`checkDocumentSize` measures operations, and `canGrow` is false for a
presence-only change), so the only ceiling left would be the 16 MiB the RPC
server reads — one request, N times its size on the way out.

`foldPresencePatches` bounds the pushed puts in the same walk that folds the
patches, so both run after the presence strip and before anything is stored,
and both see only the changes above the client's checkpoint — the ones
`pushPack` stores and every attached client then pulls. A client resending an
already-stored change is unaffected by either bound.

A `PUT` over `maxPresencePutSize` (64 KiB) loses its presence. The change
keeps its place in the pack, which is what acknowledges the push:
`CreateChangeInfos` advances the client's checkpoint over every change it is
handed. A change that also carries operations keeps them and is stored
without its presence; a presence-only one is left carrying nothing and is
persisted nowhere, so peers pull no change for it at all.

With the presence goes the cached base: the server no longer knows what
presence the sender holds, so folding its next patch onto the presence from
before the dropped put would store a presence the sender never had. The
sender's peers keep its previous presence until its next push fits, which is
what the presence path — latest-wins and lossy by design — does with any
intermediate presence.

The presence dropped here is the last one the sender set, which every other
drop on this path is not: the fold coalesces a run of patches or puts down to
the one its last change carries, and drops a patch only when a later put or
clear in the same pack replaces the presence anyway, so the sender's final
presence always reaches the store. An oversized put has no such put behind it.
The sender is therefore the replica left out of step — it holds a presence no
other replica has, while the push it made was acknowledged — and it converges
on the server's view only at its next snapshot pull, which replaces the whole
presence map (`InternalDocument.applySnapshot`) and so rolls the sender back
to the presence stored before the dropped put, or to none when no put of this
client was ever stored. That rollback is silent: the push reply carries no
field for "the presence was too large", and adding one is a protocol change
no client reads today.

`maxPresencePutPerPack` (256 KiB, 4 × the fold budget) bounds the sum of the
puts one pack may carry, for the same reason `maxPresenceFoldPerPack` bounds
the folded ones: only clientSeq continuity limits how many changes a pack
holds, so a per-put bound alone leaves a single request of a few hundred puts
at the bound reproducing the fan-out the bound exists to cap. Past the budget
the puts of a run share the single `PUT` their last change carries, so a pack
costs the budget plus one put however many it holds. It is well above both
what the fold itself lets a pack produce and a batched pack of a few hundred
ordinary presences.

Neither bound refuses the push. A client keeps a local change until the
server acknowledges it, so a pack the server refuses is pushed again
unchanged: the refusal would be permanent rather than something a smaller
presence clears, and the document — operations included — would stop syncing
for good, taking the detach and remove packs with it. Dropping the presence
is the same answer the fold gives a patch it cannot pay for.

The bound is not `maxPresenceBaseSize`. A presence above that works today — it
is never cached nor folded into, so the client pushes full puts — and bounding
it there would break clients that work now, including the ~7.4 KB presence
that motivated patches.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A stale cached base produces a presence the client does not hold | The base is used only while the client's checkpoint and epoch are exactly the ones it was cached at |
| An evicted or lost base | `ErrPresenceBaseUnavailable`; the client falls back to a full `PUT` |
| A new SDK sends a patch to an old server during a rolling upgrade | The old server stores the patch with an empty change type and serves it back as `CHANGE_TYPE_UNSPECIFIED`, which stops the sync loop of every JS reader that pulls it. Upgrade servers first and never roll a server back below this version once such clients are connected; the reply has no capability, so the SDK resends a full `PUT` and sends no further patches |
| A base kept across a detach | A detach zeroes the client's checkpoint to what a fresh attach seeds, so `commit` drops the entry when the client is no longer attached and never stores one at the initial checkpoint |
| Cache memory | LRU bounded by `--presence-base-cache-size` (default 10,000) entries, and each entry by `maxPresenceBaseSize` (4 KiB), since presence is in no document size gate. The product, about 40 MiB, is the ceiling |
| A presence of many tiny keys costing far more memory than its bytes | `presenceSize` counts `presenceEntryOverhead` (64 B) per key as well as the key and value bytes, so the 4 KiB bound also caps a presence at 64 keys and the 40 MiB ceiling holds whatever shape the presence has |
| A small patch expanding into a large stored and fanned-out put | The fold refuses a patch whose result would pass the same 4 KiB bound, so a folded put costs at most that however large a presence the sender parked earlier. The client then pushes the presence as a full `PUT`, paying for its own bytes, as every client did before patches existed |
| One oversized `PUT` the client sent being stored and pulled by every peer | The two bounds above cover only the presence the server produces, and no document size gate measures presence, so a put of any size the RPC server would read was stored verbatim and fanned out N times. A put over `maxPresencePutSize` (64 KiB) loses its presence, and the cached base with it, while the push is still acknowledged. See [Put size](#put-size) |
| The sender of a dropped `PUT` holding a presence no other replica has | Accepted, not mitigated. It is the one drop on this path that discards a client's *final* presence, so nothing behind it restores one; the push is still acknowledged and the sender rolls back to the server's view at its next snapshot pull. Refusing the push instead stops the document syncing for good, and telling the sender takes a field on the push reply that no client reads. See [Put size](#put-size) |
| One pack of many `PUT`s at the per-put bound reproducing that fan-out | The per-put bound is per put, and only clientSeq continuity limits how many changes a pack carries, so a single 16 MiB request could hold a few hundred of them. `maxPresencePutPerPack` (256 KiB) bounds the sum; past it the puts of a run share the single `PUT` their last change carries. See [Put size](#put-size) |
| One pack of many small patches expanding into as many large puts | The 4 KiB bound is per put, and nothing bounds how many changes a pack carries, so it alone would let a single request of minimal patches be rewritten into one full presence per change, each stored and fanned out. `maxPresenceFoldPerPack` (64 KiB, 16 × the per-put bound) bounds the sum of a pack's folded puts; past it the patches of a run share the single `PUT` their last change carries, so the pack's folded output is the budget plus one put however many patches it holds. See [Fold budget](#fold-budget) |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Fold on the server, pull only `PUT` | Keeps the self-healing property and needs nothing from old SDKs |
| Key the base by client ID, not actor | Two sessions of one client key share a stable actor but have separate checkpoints; keying by actor would make them invalidate each other's base |
| A dedicated cache instead of reading the presence change store | The presence store is indexed by server seq per document; finding one client's latest presence would mean a scan, and the store can be evicted independently anyway |
| Advertise on `ChangePack.capabilities` | It is the existing negotiation channel and is returned on push-pull as well as attach, so a client learns about an upgraded server without re-attaching |
| Refuse the whole push when there is no base | See [No base](#no-base) |
| Coalesce past the pack's fold budget instead of refusing | Passing the budget is a property of the pack's shape, so a refusal would recur on the resend. See [Fold budget](#fold-budget) |
| Drop the presence of an oversized `PUT` instead of refusing the push | A client keeps a local change until it is acknowledged, so a refused pack is pushed again unchanged and the document stops syncing for good, detach and remove included. See [Put size](#put-size) |
| Bound the pushed put on the push path, not in the converter | `converter.FromPresenceChange` is the decode path for stored rows as well as for the wire, so a bound there would turn a row stored before the bound into a read error on every pull |

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

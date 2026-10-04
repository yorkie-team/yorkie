---
title: pre-attach-ticket-reissue
target-version: unreleased
---

# Pre-Attach Ticket Re-issue

## Problem

A `Document` has no actor until a client attaches it. Every edit made before
`Client.Attach` runs under `time.InitialActorID`, so every ticket it mints --
an element's `createdAt`, a text or tree node's ID -- names the initial actor.

`Client.Attach` used to call `SetActor`, which rewrites only the change IDs
and each operation's `executedAt`. The root and the tickets inside the
operations kept the initial actor (`InternalDocument.SetActor` carried a TODO
saying so). Two clients that fill the same key before attaching therefore push
values with the same `createdAt`:

```go
doc1.Update(func(r *json.Object, _ *presence.Presence) error {
    r.SetNewText("k1").Edit(0, 0, "one") // value createdAt = 1:1:000...
    return nil
})
doc2.Update(...)                          // value createdAt = 1:1:000... too
```

`createdAt` is an element's identity: `Root.elementMap` is keyed by it, and
every later operation targets an element through it. Two elements sharing one
identity break the document in ways that depend on the order things arrive:

- On `main`, three rounds of the above against one server leave the root
  `{}`, and the two replicas of a round diverge.
- The Edit's `executedAt` was rewritten but the text node IDs in the local
  root were not, so the local root and the server's disagree on node IDs.
- With the "refuse a Set whose value's createdAt names a live element" rule of
  #2081/#2100, the second Set is refused, the same change's Edits land on the
  live element, and `BenchmarkRPC/attach_large_document` grows a 10 MB text by
  10 MB per iteration until compaction fails and the CI job hangs.

### Goals

- After attach, every ticket a document minted before it names the client's
  own actor, in the pushed changes and in the local root alike.
- The local root after the re-issue is the root the server builds from the
  pushed changes.
- No change for a document that has synced, or for the server, which builds
  documents with `SetActor` from state other actors wrote.

### Non-Goals

- Repairing documents already stored with colliding `createdAt`s.
- Undo/redo of edits made before the attach (see Risks).
- The JS SDK; it needs the same change as a follow-up.

## Design

`Client.Attach` calls `Document.ReissueActor(actor)` instead of `SetActor`.

```text
ReissueActor(actor):
  prev := current actor
  if prev == actor || !neverSynced() || no local changes:
      SetActor(actor)                       # legacy behavior
      return
  for each local change:
      ops := converter.ReissueOperations(ops, prev, actor)
      id  := id with actor, version vector entry prev -> actor
  root, presences := replay the new changes on a fresh root
  swap in changes, root, presences, changeID (all or nothing)
  Document: drop the clone, clear the undo/redo stacks
  return a rollback that re-issues actor -> prev, restoring the stacks
```

### When re-issuing is sound

A ticket can be re-issued only if no other replica has seen it. The
discriminator is per document, not per ticket: `neverSynced` holds when the
document has absorbed no outside state, is detached, has `InitialCheckpoint`
and a version vector naming no actor but its own. Then nothing has been pushed
and nothing pulled, so every ticket naming the current actor was minted by a
local change still in `localChanges`. The offline-resumable-attach design
rejects a client side rebase because, after a sync, the pushed/pending boundary
runs through individual tickets; that boundary does not exist before the first
sync.

The absorbed-state flag (`absorbedRemote`, set by `applySnapshot` and by
`applyChanges`) is the load-bearing half of that test, not the checkpoint: the
checkpoint is forwarded by `applySnapshot`'s *caller*, so a snapshot pack
carrying the initial checkpoint would leave status, checkpoint and version
vector all looking untouched while the root holds elements the rebuild cannot
reproduce from `localChanges` -- and the rebuild would silently drop them.

The lamport-0 ticket `time.InitialTicket` -- the root object and every sentinel
node -- is shared by all replicas and is never re-issued. Every ticket a change
mints has the change's lamport, which is at least 1.

A failed attach leaves the document never-synced under the new actor, so a
retry on another client re-issues again from that actor. `Client.Attach` also
runs the rollback `ReissueActor` returned when the attach fails, so the user is
left holding the document handed over -- root, local changes and undo/redo
stacks included -- rather than one rewritten for an attach that never happened.

The rollback is the same re-issue run the other way, `actor -> prev`, rather
than a restore of a snapshot taken when the tickets were minted. The attach
makes local changes of its own after that point -- the initial presence PUT --
and an application goroutine may call `Update` while the round trip is in
flight; a snapshot restore would drop both, while a reverse re-issue carries
them back with it. `neverSynced` guards the rollback exactly as it guards the
forward re-issue: it stops holding the moment the document takes the server's
attach pack in, which is the state a rollback must not overwrite. Document
status is no guard there -- `attachDocument` sets `StatusAttached` only *after*
the pack is applied, and puts it back to `StatusDetached` when it hands an
already-applied attach to a concurrent `Deactivate`. An undo entry pushed
during the attach window is dropped by the restore: its reverse operations name
tickets the re-issue minted and the rollback has just re-issued away.

`neverSynced` is a purely local signal, though, and there is a window it cannot
see: between `AttachDocument` returning and `ApplyChangePack` absorbing the
response, the server already holds the re-issued elements while the checkpoint
is still initial, the status still `StatusDetached` and nothing absorbed. A
failure in that window -- decoding the response pack, bringing the watch
pipeline up, the post-attach `Update` or `ClearHistory` -- would pass the guard
and re-issue away tickets the server has stored. `attachDocument` therefore
reports whether the push reached the server, and `Client.Attach` records it on
the document with `MarkPushed` when it did. A transport error counts as not
reached even though the server may have committed before the response was lost:
re-attaching with the same client re-issues the same tickets to the same actor,
so the replicas still name those elements identically.

The mark lives on the document rather than in the attach that set it, because
the forward re-issue reads the same blind signals as the rollback. A document
left behind by a pushed-but-failed attach is still `StatusDetached` with an
initial checkpoint and nothing absorbed, so attaching it again -- with the same
client after a reactivation, or with another client altogether -- would re-issue
to a second actor the very elements the server already holds under the first.
`neverSynced` therefore reports false once the mark is set, in both directions:
the rollback declines, and a later `ReissueActor` falls back to `SetActor`,
which stamps the new actor into the change IDs while leaving every stored ticket
as the server knows it.

### Re-issuing the operations

`converter.ReissueOperations` converts the operations to protobuf, rewrites
the actor of every `TimeTicket` it reaches with a `protoreflect` walk, and
converts them back. The wire format is what the server decodes, and the walk
covers every ticket the protocol carries -- `parentCreatedAt`, positions, node
IDs, TreeEdit contents and split tickets, restore spans -- without a per-type
list that a new field could fall out of. Object, Array and Tree values travel
as the bytes of an `api.JSONElement`; the walk decodes, rewrites and re-encodes
them. Lamports and delimiters are untouched, so the order among the tickets is
unchanged.

One value does not survive the wire: a Text travels without its content, which
normally arrives through the Edits that follow. A Set/Add/ArraySet that
restores a removed Text -- the reverse of a Remove, run by Undo -- carries the
content in the value itself. For those operations the Text value is re-issued
through its full snapshot encoding instead, so the rebuilt local root keeps the
content and a later Edit on its nodes still replays. The server still receives
the Text empty; that wire gap predates this design and is listed under Risks.

### Rebuilding the root

Instead of rewriting tickets inside the root -- `elementMap`, the GC maps,
split-tree and tree node indexes all key on them -- the root and presences are
rebuilt by replaying the re-issued changes on a fresh root. A never-synced
document's root is exactly the initial root plus its local changes, and the
replay is the same computation the server performs on the pushed changes, so
the two roots agree by construction. The presence map comes out keyed by the
new actor.

The undo/redo stacks hold reverse operations naming the old tickets, so a
re-issue clears them.

### Server-side actor ownership

With the re-issue, every ticket and every change ID a client pushes carries
its own actor; before it the change ID already did (`SetActor` rewrote it),
only the tickets inside did not. That makes the actor of a pushed change
enforceable: `PushPull` refuses (`InvalidArgument`, `ErrInvalidChangeActor`) a
not-yet-acknowledged change whose actor the client does not own
(`ClientInfo.IsOwnActor`), so the pull dedup and the `DocChanged` publisher
can trust a stored change's actor. See offline-resumable-attach.md, "Pushed
change actors must be owned by the pusher", for the legacy analysis.

The gate is bounded by how the acting client is identified, which this design
does not change: every document RPC resolves the client from the request's
`client_id` and `clients.FindActiveClientInfo` only checks that the row exists
and is activated, so authentication is project-scoped (API key, optional auth
webhook) rather than per client. A caller inside the same project can still
satisfy the ownership predicate for another client, by either identity
`IsOwnActor` accepts: it can present that client's session id if it learns one,
or -- for the `StableActorID` branch -- activate with that client's key, which
is an application-chosen name the system has never treated as a secret and
which `types.DeriveActorID` turns into the same actor every time. Neither is
new: such a caller could already act as that client everywhere else in the API,
since `client_id` is what every document RPC resolves the actor from. Per
client credential binding is a protocol-level change and a separate task; the
gate still removes the cross-client actor forgery that needed no client
identity at all.

### Security boundary

Stating the above as a boundary, because `validateChangeActors` reads like an
authorization control and is not one:

- **What it enforces.** For a not-yet-acknowledged change in a pushed pack, the
  change's actor must be one of the two identities of the `client_id` the
  request carries. It removes the variant of actor forgery that needs no
  knowledge of the victim at all: stamping an actor that matches neither of the
  pusher's own identities.
- **What it does not enforce.** It is satisfied by whoever presents the
  victim's `client_id`, because `clients.FindActiveClientInfo` resolves that id
  without any credential check and the surrounding auth is project-scoped. It
  is equally satisfied by a caller acting under a `client_id` of its own:
  `StableActorID` is `types.DeriveActorID(projectID, clientKey)` and
  `ActivateClient` mints a new row for a client key already in use, so
  activating under the victim's client key yields a client that genuinely owns
  the victim's stable actor. The client key is an identifier, not a secret, and
  resuming one actor across sessions is the whole point of the derivation.
  Co-tenants of a project are therefore not isolated from each other by this
  gate, exactly as they are not isolated by any other document RPC. The same
  two bypasses apply to the Watch `actor_id` check (`ErrActorMismatch`), which
  compares the declared actor against the same `StableActorID`. Closing either
  needs per-client credentials at the protocol level: a separate task.
- **What it does not cover.** The pack's other client-supplied identity input,
  `ChangePack.VersionVector`, is stored verbatim by `UpdateMinVersionVector`
  and fed to min-VV and GC. Ownership is the wrong predicate for it -- a
  version vector legitimately carries other actors' lamports -- so membership
  is left unchecked: nothing verifies that an entry names an actor that ever
  touched the document. Two things bound the exposure, and only two:
  - **Size.** `validateVersionVectorSize` caps the entry count
    (`maxVersionVectorEntries`). Without it the count is limited only by the
    16 MiB pack, and every entry is persisted in the pusher's
    `VersionVectorInfo` row, held in the per-document `vectorCache` and
    unioned into the minVV returned to every other client of that document.
  - **Direction, partly.** The vector is stored under the pusher's own row,
    and `MinVersionVector` treats an actor missing from any row as `0`, so a
    forged lamport generally drags the minimum down -- stalling tombstone GC
    on documents the pusher is attached to -- rather than raising it past
    another client's own row. This assumes every participant has a
    `VersionVectorInfo` row to clamp against; clients attached with
    `DisableGC` have none (`updateVersionVector` skips them, see
    `disable-gc-on-attach.md`), so their position does not hold the minimum
    back. That is what the GC opt-out already asks for -- tombstones need not
    be kept alive for them -- but it means the clamp argument covers
    GC-tracked clients only.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Undo of a pre-attach edit is no longer possible after attach (user-visible behavior change) | Documented on `Document.ReissueActor`. Before this change such an undo already produced Edits whose node IDs the server did not know |
| A pre-attach Undo that restored a removed Text pushes that Text empty | Existing wire gap (`toJSONElementSimple` sends no Text content); the local root keeps the content. Fixing the encoding is a protocol change for a separate task |
| An undo change keeps operations that were skipped locally; the replay would run them | `executeUndoRedo` now buffers only the operations that executed, so the change carries nothing the undo declined to apply -- on the replay or on the wire |
| Replay of a large pre-attach document costs time at attach | One replay of the local changes, the same work the server does on push |
| A conversion or replay error | The document is left untouched and `Attach` returns the error before any RPC |
| Documents stored before the fix still hold colliding `createdAt`s | Out of scope; new attaches no longer create them |

### Design Decisions

| Decision | Reason |
|----------|--------|
| A new `ReissueActor`, not a smarter `SetActor` | The server builds documents with `SetActor` from snapshots that legitimately hold initial-actor elements written by the server itself; only the attaching client may re-issue |
| Guard on "never synced" rather than a lamport watermark | Before the first sync every ticket of the current actor is local; a watermark is unsound once pulls bump the lamport |
| Rewrite through protobuf | One generic walk over the wire format reaches every ticket; a per-operation rewrite would have to track every crdt type's ticket fields |
| Rebuild the root by replay | Matches the server by construction; rewriting the root in place means re-keying every index |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Rewrite tickets in place in the root and the operations | Touches `elementMap`, GC maps, RGA split and tree node indexes and every operation type; easy to miss one, and a miss is silent divergence |
| Reject a pre-attach edit, or require attach before edit | Breaks the documented offline-first usage of both SDKs |
| Server-side de-duplication of colliding `createdAt`s | The server cannot tell which client's later operations target which element |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

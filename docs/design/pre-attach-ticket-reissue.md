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

`Client.Attach` calls `Document.SetActorWithOptions(actor, document.WithReissue())`
instead of `SetActor`. The API:

```go
type SetActorOption func(*setActorOptions)

func WithReissue() SetActorOption

func (d *Document) SetActorWithOptions(actor time.ActorID, opts ...SetActorOption) error
```

Without options `SetActorWithOptions` is `SetActor` and returns nil. With
`WithReissue` it re-issues a never-synced document's tickets as below, and
falls back to plain `SetActor` for a document that has synced.
`attachable.Attachable.SetActor` and every existing `SetActor` caller -- the
server's snapshot path, channels, test cases -- are unchanged.

```text
SetActorWithOptions(actor, WithReissue()):
  prev := current actor
  if prev == actor || !neverSynced() || no local changes:
      SetActor(actor)                       # legacy behavior
      return
  for each local change:
      ops := converter.ReissueOperations(ops, prev, actor)
      id  := id with actor, version vector entry prev -> actor
  root, presences := replay the new changes on a fresh root
  swap in changes, root, presences, online clients, changeID
      (all or nothing; nothing is written in place)
  Document: drop the clone, clear the undo/redo stacks
```

### When re-issuing is sound

A ticket can be re-issued only if no other replica has seen it. The
discriminator is per document, not per ticket: `neverSynced` holds when the
document is detached, has absorbed no snapshot and no applied change
(`absorbedRemote`, set by `applySnapshot`, `applyChanges` and
`NewInternalDocumentFromSnapshot`, and kept by `DeepCopy`), its checkpoint is
`InitialCheckpoint` and its version vector names no actor but its own. The
absorbed flag matters because a snapshot pack carrying the initial checkpoint
would leave the other signals looking untouched. Then nothing has been pushed and nothing
pulled, so every ticket naming the current actor was minted by a local change
still in `localChanges`. The offline-resumable-attach design rejects a client
side rebase because, after a sync, the pushed/pending boundary runs through
individual tickets; that boundary does not exist before the first sync.

The lamport-0 ticket `time.InitialTicket` -- the root object and every sentinel
node -- is shared by all replicas and is never re-issued. Every ticket a change
mints has the change's lamport, which is at least 1.

### One actor, two never-synced documents of one key

`neverSynced` holds for a `Document` value, and the re-issue keeps each
ticket's lamport, which starts at 1 in every fresh document. So the result is
unique between clients -- the point of the design -- but not between two
never-synced documents of the same key under one actor:

```go
first := document.New(k); first.Update(...); cli.Attach(ctx, first); cli.Detach(ctx, first)
second := document.New(k); second.Update(...); cli.Attach(ctx, second)
```

`second`'s tickets would be re-issued to the same actor with the same lamports
`first` already pushed -- the `createdAt` collision again, this time between
one client's own elements. `Client.claimReissue` records, per key, the actor
the client last attached it under, and declines the re-issue when that actor
is attaching the same key again. Those tickets keep the initial actor, as they
did before this design; they can still collide with another client's
pre-attach tickets, which is strictly the state `main` is in, rather than the
certain collision a re-issue would mint. The mark is taken before the round
trip, since an attach whose response is lost may still have pushed, and is
never cleared: a detach does not take pushed tickets back.

The mark lives on the `Client`, so it does not carry across processes. A new
client that reactivates with an explicit `WithKey` takes the same client id --
hence the same actor -- and would re-issue a fresh document of a key it
attached in an earlier process. Closing that needs state the client does not
hold before the attach round trip (the server's lamport for the key); it is
filed as a follow-up with the client identity model in #2114.

### A failed attach

There is no rollback. A re-issued document is a valid detached document
whatever the attach does next, so it keeps the re-issued state:

- a retry under the same client finds `prev == actor` and re-issues nothing;
- a retry under another client re-issues from this actor to its own.

Rolling back on failure could not be made safe. When the `AttachDocument`
response is lost, the client cannot tell whether the server stored the
re-issued pack. Restoring the initial actor would then let a later attach
push the same changes a second time under the initial actor -- the very
`createdAt` collision this design removes. Keeping the re-issued state means a
retry under another client may still push those changes twice, but as two
distinct elements that LWW resolves, never as one identity shared by two.
That duplicate on an unknown outcome predates this design: the old `SetActor`
path pushed the changes again too, with colliding tickets.

The only thing a failed attach loses is the pre-attach undo/redo history, which
a successful attach clears anyway (`Client.attachDocument`).

### Re-issuing the operations

`converter.ReissueOperations` converts the operations to protobuf, rewrites
the actor of every `TimeTicket` it reaches with a `protoreflect` walk, and
converts them back. The wire format is what the server decodes, and the walk
covers every ticket the protocol carries -- `parentCreatedAt`, positions, node
IDs, TreeEdit contents and split tickets, restore spans -- without a per-type
list that a new field could fall out of. Object, Array and Tree values travel
as the bytes of an `api.JSONElement`; the walk decodes, rewrites and re-encodes
them. Lamports and delimiters are untouched, so the order among the tickets is
unchanged. A string-keyed map entry naming the old actor (hex or base64 form)
is renamed too; no operation encodes one today -- `created_at_map_by_actor` is
deprecated and never written -- but the walk does not leave one behind if it
comes back.

One value does not survive the wire: a Text travels without its content, which
normally arrives through the Edits that follow. A Set/Add/ArraySet that
restores a removed Text -- the reverse of a Remove, run by Undo -- carries the
content in the value itself. For those operations the Text value is re-issued
through its full snapshot encoding instead, so the rebuilt local root keeps the
content and a later Edit on its nodes still replays. The server still receives
the Text empty; that wire gap predates this design and is listed under Risks.
A dedup Counter value travels without its HLL registers the same way; it is
not special-cased, so the local root matches what the server receives.

### Rebuilding the root

Instead of rewriting tickets inside the root -- `elementMap`, the GC maps,
split-tree and tree node indexes all key on them -- the root and presences are
rebuilt by replaying the re-issued changes on a fresh root. A never-synced
document's root is exactly the initial root plus its local changes, and the
replay is the same computation the server performs on the pushed changes, so
the two roots agree by construction. The presence map comes out keyed by the
new actor, and an online-client entry for the old actor is renamed.

The undo/redo stacks hold reverse operations naming the old tickets, so a
re-issue clears them.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Undo of a pre-attach edit is no longer possible after attach | A successful attach already clears the history; the re-issue moves that point before the RPC, so a failed attach loses it too |
| A lost `AttachDocument` response, retried under another client, pushes the pre-attach changes twice | Pre-existing (the old path did the same, with colliding tickets); now the two copies are distinct elements |
| A pre-attach Undo that restored a removed Text pushes that Text empty | Existing wire gap (`toJSONElementSimple` sends no Text content); the local root keeps the content. Fixing the encoding is a protocol change for a separate task |
| An undo change keeps operations that were skipped locally; the replay runs them | The rebuilt root then matches what the server builds from the same change, not the pre-attach view. Predates this design |
| Replay of a large pre-attach document costs time at attach | One replay of the local changes, the same work the server does on push |
| A conversion or replay error | The document is left untouched and `Attach` returns the error before any RPC |
| Documents stored before the fix still hold colliding `createdAt`s | Out of scope; new attaches no longer create them |
| `SetActor`, the fallback, rewrites shared change values in place | Pre-existing; the re-issue path replaces every structure instead, and no caller deep copies a document holding local changes |
| The server trusts a pushed change's actor | Out of scope, tracked in #2114 |

### Design Decisions

| Decision | Reason |
|----------|--------|
| An opt-in `WithReissue` option, not a smarter `SetActor` | The server builds documents with `SetActor` from snapshots that legitimately hold initial-actor elements written by the server itself; only the attaching client may re-issue. An option of `SetActor` rather than a second verb keeps the attachable interface unchanged; a functional option rather than a bare bool follows the Uber style guide |
| Guard on "never synced" rather than a lamport watermark | Before the first sync every ticket of the current actor is local; a watermark is unsound once pulls bump the lamport |
| Rewrite through protobuf | One generic walk over the wire format reaches every ticket; a per-operation rewrite would have to track every crdt type's ticket fields |
| Rebuild the root by replay | Matches the server by construction; rewriting the root in place means re-keying every index |

## Out of Scope

The server does not bind a pushed change's actor to the authenticated client,
so a forged actor can make another client drop a change on pull. Every change
this design pushes carries the client's own actor, which makes a push-side
check possible, but the client identities such a check would rely on
(`client_id`, `StableActorID`) are not credentials. Tracked in #2114.

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Rewrite tickets in place in the root and the operations | Touches `elementMap`, GC maps, RGA split and tree node indexes and every operation type; easy to miss one, and a miss is silent divergence |
| Reject a pre-attach edit, or require attach before edit | Breaks the documented offline-first usage of both SDKs |
| Server-side de-duplication of colliding `createdAt`s | The server cannot tell which client's later operations target which element |
| Roll the re-issue back when the attach fails | Unsafe when the outcome is unknown (see "A failed attach"), and it only preserves history a successful attach clears anyway |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

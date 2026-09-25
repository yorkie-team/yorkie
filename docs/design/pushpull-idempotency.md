---
title: pushpull-idempotency
target-version: 0.6.48
---

# PushPull Idempotency

## Problem

`packs.PushPull` writes to two places through two separate database calls:

1. `pushPack` → `CreateChangeInfos` stores the pushed changes and advances
   `DocInfo.ServerSeq`.
2. `pullPack` → `UpdateClientInfoAfterPushPull` advances the client's
   checkpoint on `ClientInfo` (`ClientDocInfo.ServerSeq` / `ClientSeq`).

Nothing makes the pair atomic. When the first succeeds and the second fails —
or the response is lost on the way back — the changes are durable but the
client's `ClientSeq` still points before them. The client, which saw no
response, retries the same pack.

The only duplicate filter `pushPack` had read that same checkpoint:

```go
if cn.ID().ClientSeq() <= cpBeforePush.ClientSeq { continue }
```

so on the retry every change passed the filter and was stored a second time
under fresh server seqs. Pullers then apply it twice, and operations that are
not idempotent (`increase`, text edits) double-count. Reported as
[#1001](https://github.com/yorkie-team/yorkie/issues/1001), related to #805.

### Goals

- A retried pack whose operation-carrying changes are already stored must not
  store them again, must not advance `DocInfo.ServerSeq` for them, and must
  return a checkpoint that acknowledges them so the client stops retrying.
  Changes the filter cannot see (no operations — see Risks) are outside this
  goal and still burn a `server_seq`.
- A dropped duplicate must not be re-applied anywhere else in the request: it
  is removed from `reqPack.Changes` as well as from the write, because
  `pullSnapshot` replays that slice on top of a document already built through
  `initialSeq`, which holds the stored copy.
- A change that is durable but was never announced — stored by the attempt that
  then failed — is announced by the retry that drops it, so the `DocChanged`
  event, the `DocRootChanged` webhook and the snapshot trigger are not lost.

### Non-Goals

- Making the two writes atomic. That is the real cure and needs either a
  transaction spanning the `changes` and `clients` collections or a redesign
  that writes the acknowledgement alongside the changes.
- Deduplicating anything other than a verbatim re-send of changes this server
  already stored for the same document.

## Design

The `changes` collection is the authoritative record of what was stored, so
`pushPack` asks it instead of trusting the checkpoint alone. Inside the
`DocPushKey` lock, where a fresh `DocInfo` is already in hand,
`filterStoredChanges` looks up the pushing client's own actor's latest stored change with
the existing `Database.FindLatestChangeInfoByActor` and drops any pushable
that is at or behind it in **both** `ClientSeq` and `Lamport`:

```go
if info.ClientSeq <= latest.ClientSeq && info.Lamport <= latest.Lamport {
    // already stored — drop
}
```

Dropped changes are acknowledged by advancing the checkpoint that is handed to
`CreateChangeInfos`:

```go
cpBeforePush = cpBeforePush.SyncClientSeq(maxStoredClientSeq)
```

Without this the response would repeat the stale `ClientSeq` and the client
would re-send the same pack forever. With it, `pullChangeInfos` also filters
those changes out of the pull, by the self-echo rule it already applies
(`clientInfo.IsOwnActor(...) && cpAfterPush.ClientSeq >= pulledChange.ClientSeq`).

### Why the pair, and not either field

`ClientSeq` is scoped to an *attachment*, not to an actor. `DetachDocument`
zeroes it and `AttachDocument` re-seeds it, so a client that detaches and
re-attaches under the same `StableActorID` starts again at 1 while the
document still holds its previous session's changes. Matching on `ClientSeq`
alone would discard those new changes.

`Lamport` is what survives an attachment boundary: attach syncs the client's
clock up to the document's maximum before it produces any change, so a
re-attached client's changes carry lamports above everything stored for it.
Matching on `Lamport` alone is likewise not enough, because a duplicate is
behind on both and only requiring both narrows the predicate to an actual
re-send.

### Skips

| Skip | Why |
|------|-----|
| `DocInfo.ServerSeq == cpBeforePush.ServerSeq` | A duplicate can only have been stored after this client was last acknowledged. If the document has not moved since, no duplicate exists — no query needed, which keeps the single-writer steady state free of the extra round trip. |
| A *fresh* attach (`IsAttach` **and** a seeded checkpoint of 0/0) | A fresh attach may carry local edits made before the attach, at `ClientSeq` 1 / `Lamport` 1 — below what the same stable actor stored during an earlier attachment, and therefore indistinguishable from a re-send. A resumed (Case-B) attach seeds the presented checkpoint verbatim (`ClientInfo.AttachDocument`), so its `ClientSeq` does not restart and it is **not** skipped: it is a re-send path like any other sync. |
| A change whose actor is not the pushing client's own (`ClientInfo.IsOwnActor`) | `ActorID` arrives verbatim from the wire and no push path proves that a change belongs to the actor it names. Keying a silent drop plus a forward checkpoint acknowledgement on a foreign actor would let one client suppress another's writes by raising the stored `(ClientSeq, Lamport)` watermark under the victim's actor. It also bounds the work under the exclusive `DocPushKey` lock to one lookup per pack rather than one per distinct actor in it, so a large pack of forged actors cannot amplify into N round trips. |
| A change carrying no operations | `change.Context.ToChange` builds those via `ID.Next(true)`, which leaves `Lamport` at 0. No stored lamport sits below 0, so `info.Lamport <= latest.Lamport` would hold for free and drop presence-only changes — the cluster detach presence clear, or any presence update from a client that re-attached — that are not re-sends at all. |
| A server-side client (`ClientInfo.IsServerClient`) | `database.SystemClientInfo` seeds `ClientSeq` 0 on every call, so every admin `UpdateDocument` / revision restore stamps `InitialActorID` with `ClientSeq` 1. A second update would otherwise look like a re-send of the first whenever the document's `ServerSeq` moved in between. |

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Extra database round trip on the push hot path | One indexed `FindOne` per distinct actor in the pack (in practice one), gated on the document having moved since the client's last acknowledgement. The lookup uses the existing `project_id/doc_id/actor_id/server_seq` index. |
| A legitimate change is mistaken for a duplicate and silently dropped | Requires being behind on both `ClientSeq` and `Lamport`, which a client cannot produce after syncing, *and* being stamped with the pushing client's own actor. The one case that can — pre-attach local edits — is excluded via the fresh-attach skip. Every drop is logged at warn level with actor, clientSeq and lamport. |
| A fresh attach that fails after `CreateChangeInfos` and is retried stores its pre-attach changes twice | Not closed. `ClientInfo.AttachDocument` only rejects with `ErrDocumentAlreadyAttached` once the status is persisted as `DocumentAttached`, while the interrupted attempt leaves it at `Attaching` (`clients.TryAttaching`), so the retry reaches `PushPull` and is skipped by the fresh-attach rule above. Closing it needs a discriminator the metadata does not carry (the pre-attach edits and the re-send are identical in `ClientSeq`/`Lamport`); atomicity — the recorded non-goal — is the real fix. |
| The two backends disagree about what the `changes` collection holds | Mongo routes presence-only changes to `presenceCache` and stores nothing for a change with neither operations nor presence; the memory backend inserts every change into `tblChanges`, including presence-only rows whose `Lamport` is 0. The filter therefore treats "found" as *the row names an actor*, never as `Lamport > 0`, and a presence-only latest row simply matches nothing — the change is re-stored rather than silently dropped. Dedup coverage of operation-carrying changes is identical on both. |
| Two concurrent sessions under one client key share a `StableActorID`, so one session's changes can sit behind the other's in both fields | Out of scope here, and already broken without this filter: a shared actor also breaks self-echo dedup, version-vector liveness and lamport ordering. One logical client at a time is the assumption `StableActorID` is built on (see [Offline-Resumable Attach](offline-resumable-attach.md)). |
| Compaction removes the stored changes a duplicate would be matched against | Compaction bumps `DocInfo.Epoch`, and the epoch check in `pushPack` discards stale-epoch changes before this filter is reached. |
| A re-sent change that carries no operations is not recognised | `FindLatestChangeInfoByActor` reads the `changes` collection, and the Mongo backend writes only changes that carry operations: `CreateChangeInfos` routes presence-only changes to `presenceCache` and stores nothing at all for a change with neither, keeping only the `server_seq` it consumed. Re-storing either duplicates no operation — presence is last-write-wins and an empty change carries nothing — so the only cost is a burnt `server_seq`. The double-counting this design exists to stop (`increase`, text edits) is confined to operation-carrying changes, which are exactly the ones the filter sees. |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Filter in `pushPack`, under the existing `DocPushKey` lock | The lock already serialises pushes for the document, so the lookup and the insert observe the same state. No new lock, no new ordering. |
| Reuse `FindLatestChangeInfoByActor` | It already exists for the cluster detach path and is backed by an index on both database implementations; one row is enough, since a duplicated batch is bounded above by the actor's latest stored change. |
| Acknowledge rather than reject | Returning an error for a retry would leave a client that did nothing wrong permanently stuck; advancing the checkpoint is what lets it converge. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Unique index on `(project_id, doc_id, actor_id, client_seq)` | A migration, and wrong as stated: `client_seq` restarts across attachments, so the index would reject legitimate changes from a re-attached client. |
| Compare change content (operations, message) instead of metadata | Requires decoding every stored operation on the push path, and the metadata pair already separates re-sends from new work everywhere except the attach case, which is excluded outright. |
| Make PushPull atomic | The correct fix, but far larger: it spans two collections and both database implementations. Recorded as a non-goal here. |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents:
`20260925-pushpull-duplicate-changes-todo.md`.

# PushPull: a retried pack re-stores changes the server already has

**Created**: 2026-09-25

Issue #1001. `ClientSeq` on `ClientInfo` is the only thing that decides
whether a pushed change is new, and it is written by a different database
call than the changes themselves.

## Problem

`packs.PushPull` is not atomic. `pushPack` calls `CreateChangeInfos` to store
the changes and bump `DocInfo.ServerSeq`; `pullPack` then calls
`UpdateClientInfoAfterPushPull` to advance the client's checkpoint. When the
first succeeds and the second fails, the changes are in the `changes`
collection but the client's `ClientSeq` still points before them, and the
client — which never saw a response — retries the same pack.

On the retry `pushPack`'s only duplicate filter is

```go
if cn.ID().ClientSeq() <= cpBeforePush.ClientSeq { continue }
```

which reads the stale checkpoint, so every change in the pack is stored a
second time under fresh server seqs. Non-idempotent operations (`increase`,
text edits) are then applied twice by every puller.

`server/packs/pushpull_test.go` already carries the reproduction as a skipped
subtest, "cannot detect change duplication due to clientInfo update failure",
with `t.Skip("remove this after resolving pushpull consistency problem")`.

## Design

Ask the authoritative record — the `changes` collection — instead of trusting
only the checkpoint. Inside the `DocPushKey` lock, where `pushPack` already
holds a fresh `DocInfo`, look up each pushing actor's latest stored change via
the existing `FindLatestChangeInfoByActor` and drop any pushable that is at or
behind it in **both** `ClientSeq` and `Lamport`.

Both halves are needed:

- `ClientSeq` alone over-matches. A client that re-attaches under the same
  stable actor restarts `ClientSeq` at 1 while the document still holds its
  previous session's changes.
- `Lamport` alone over-matches too — it is not unique per actor across
  sessions in the same way, and a duplicate always matches on both.

A re-attached client syncs its lamport up to the document's maximum before it
produces a new change, so its post-attach changes sit above everything stored;
the pair only matches an actual re-send.

When changes are dropped, `cpBeforePush` is advanced with `SyncClientSeq` to
the highest dropped `ClientSeq`, so the response finally acknowledges them and
the client stops re-sending. `pullChangeInfos` then filters those same changes
out of the pull for the same reason it already filters self-echo.

Two guards keep the extra query off the hot path and out of the one case
where a restarted `ClientSeq` is legitimate:

- Skip when `DocInfo.ServerSeq == cpBeforePush.ServerSeq`: nothing has been
  stored since this client was last acknowledged, so no duplicate can exist.
- Skip on the `AttachDocument` path (`PushPullOptions.IsAttach`) when, and only
  when, the seeded checkpoint is still 0/0. A fresh attach legitimately pushes
  pre-attach local edits at `ClientSeq` 1 / `Lamport` 1, which an earlier
  attachment's stored changes would shadow. A resumed (Case-B) attach seeds the
  presented checkpoint verbatim, so it is not skipped.

  **Known open risk, not closed by this change**: a fresh attach that fails
  after `CreateChangeInfos` and is retried is exempt too, so its pre-attach
  changes can be stored twice. `ClientInfo.AttachDocument` only rejects with
  `ErrDocumentAlreadyAttached` once the status is persisted as
  `DocumentAttached`, while the interrupted attempt leaves it at `Attaching`
  (`clients.TryAttaching`), so the retry does reach `PushPull`. The pre-attach
  edits and the re-send are identical in `ClientSeq`/`Lamport`, so no
  discriminator exists in the metadata; atomicity is the real fix. Recorded in
  the Risks table of `docs/design/pushpull-idempotency.md`.

A drop additionally requires an **anchor**: the pack must contain the actor's
latest stored change verbatim (same `ClientSeq`, same `Lamport`). A genuine
re-send always carries it. Without the anchor, any pack sitting below the
actor's watermark would be discarded — reachable when two sessions share one
`StableActorID`, or when that watermark was raised by someone else.

## Tasks

- [x] `filterStoredChanges` in `server/packs/pushpull.go`, called from
      `pushPack` under the `DocPushKey` lock.
- [x] Advance `cpBeforePush` past the dropped changes.
- [x] `PushPullOptions.IsAttach`, set at the `AttachDocument` call site.
- [x] Un-skip the reproduction subtest in `server/packs/pushpull_test.go`.
- [x] `docs/design/pushpull-idempotency.md` + `docs/design/README.md` entry.
- [x] Require the anchor before dropping, so a watermark this pack did not set
      cannot silence it.
- [x] Drop already-acknowledged changes from `reqPack.Changes` too: `pullSnapshot`
      replays that slice over a document already built through `initialSeq`.
- [x] Make the memory backend's `FindLatestChangeInfoByActor` skip the
      presence-only rows Mongo never writes to the changes collection, so the
      filter is not silently disabled there, and teach the other caller
      (`clusterServer.DetachDocument`) that `ErrChangeNotFound` means "no stored
      change", not failure.

## Out of scope

- Making PushPull atomic (the real cure; needs a transaction across two
  collections, or a write of the checkpoint alongside the changes).
- A unique index on `(project_id, doc_id, actor_id, client_seq)`: a migration,
  and wrong as stated because `client_seq` restarts across attachments.

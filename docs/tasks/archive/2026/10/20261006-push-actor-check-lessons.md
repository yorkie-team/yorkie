**Created**: 2026-10-06

# Refuse Pushed Changes Stamped With Another Client's Actor — Lessons

Plan: `20261006-push-actor-check-todo.md`.

## Who stamps which actor

Before refusing a foreign actor, every honest writer had to be shown to stamp
one that `IsOwnActor` accepts:

- **Go client.** `Client.Attach` calls `SetActorWithOptions(loadID())` before
  `attachDocument` builds the pack. `loadID()` is the session id. With or
  without `WithReissue`, `SetActor` rewrites the ID of every local change, so
  pre-attach changes leave with the session id; only the tickets inside
  operations may keep the initial actor (when `claimReissue` declines), and
  those are out of scope. A reactivation takes a new session id, and the next
  `Attach` stamps it again.
- **JS SDK.** `attach` and `remove` call `doc.setActor(actorID ?? id)` before
  `createChangePack`. `actorID` is `ActivateClientResponse.actorId` (the
  stable actor), or the session id against an old server or after a
  channel-first activation. The offline restore runs after `setActor` and
  `restoreFromBytes` throws when the persisted changeID names another actor,
  so a restored pack carries the current actor too.
- **Server writers.** Revision restore and admin document update push
  `InitialActorID` changes as `SystemClientInfo`, whose ID is
  `InitialActorID`. The cluster `DetachDocument` builds its presence-clear
  change with the session id from the request.

## Where the check goes

`PushPull` step 00, next to `validateClientSeqContinuity`: it runs before the
presence strip and before `pushPack` writes, for all four RPC handlers, the
cluster detach and the server writers at once.

Only changes above the checkpoint are checked, because only those are
stored; `pushPack` drops the rest before they reach the database or the
publisher.

## What the compare is worth, and where it stops

The decisive question was what an attacker actually holds. The two identities
the compare reaches are not symmetric:

- A **stable actor** is on the wire. Every change a peer pulls carries its
  author's actor (`converter.ToChangePack`), so any collaborator in a document
  holds every other participant's actor. It is `DeriveActorID(project_id,
  client_key)` — a digest with no preimage — so holding it yields neither the
  client key nor a `client_id`.
- A **`client_id`** is not published to peers, and is not a credential either:
  whoever holds one is that client as far as every RPC is concerned.

So the realistic attacker is a collaborator that knows a victim's actor and
nothing else, and the compare is exactly what stops it: it cannot resolve a
client row that holds that actor. That is also the compare the Watch path
already enforces (`yorkie_server.go` Watch, `ErrActorMismatch`, "so a client
cannot subscribe under another client's presence identity"), and leaving the
push path open made that gate reachable around — a pushed change sets presence
keyed on its own actor (`internal_document.go` `applyPresenceChange`) for every
peer, while the victim drops it as a self-echo.

What the compare does not reach stays recorded as a limit rather than a
guarantee: a caller already holding a victim's `client_id` or client key, and
two honest sessions of one client key, which share a stable actor by design.
Both need an authenticated client identity (#2114).

Where a rejection could not go, the log stayed:

- The **initial actor** is accepted. A client that never calls `SetActor`
  pushes pre-attach changes under it, a declined pre-attach re-issue leaves
  initial-actor tickets behind, and the server's own writers hold it as their
  session id. It is also no client's own actor on pull, so a change stamped
  with it reaches every client and suppresses none — accepting it gives up no
  part of #2120.
- An operation's **`executedAt`** ticket keeps the actor it was minted under,
  which a declined re-issue legitimately leaves as the initial actor, and
  neither the pull dedup nor the presence keying reads it. A mismatch there is
  warned about, not refused.

## Proving the tests

`validateChangeActors` is unexported but pure, so the gate is pinned by a
unit test (`server/packs/push_actor_internal_test.go`, no build tag, runs in
`make verify`): own session id, own stable actor, the initial actor, the server
client row and an already-acknowledged change pass; another client's actor in a
change ID returns `ErrActorMismatch`, and in an operation ticket does not.
`logging.From(context.Background())` returns a nil logger until
`logging.DefaultLogger()` has been called once, so the test seeds the context
with it — the warn path panics otherwise.

`TestPushForeignActor` in `server/packs` pins the same thing over the wire,
including the error code, plus the accepted side (initial actor, the pusher's
own two identities) on push and on attach.

The #2120 forgery is an integration test that runs: the forged `{"x":1}` is
refused with `ErrActorMismatch`, and the same change under the pusher's own
actor reaches the victim.

The integration test first re-attached a fresh document under the same actor.
That reproduces the pre-attach re-issue design's known gap (the pull filter
skips the actor's own earlier changes), not this change, so the test uses a
reactivation, which takes a new session id, instead.

## Environment

Another agent's integration run held ports 11101/11201 for a while, and
`make verify` failed in `TestWatchAccessRevalidation` on "address already in
use". Re-running once the ports were free passed; it was not this change.

### The `ErrEpochMismatch` integration failure is a doc-cache race

`TestAuthWebhookPresenceOnly/a reader attaching alone keeps the bound schema`
failed in one integration run at `auth_webhook_test.go:1037`, where a writer
re-attaches a fresh `document.New(docKey)`:

```text
discarding 1 changes from stale epoch: client(1) != doc(0)
client epoch(1) != document epoch(0): epoch mismatch
```

A client epoch *above* the document's is the signature of a stale `docCache`
entry, not of a stale client:

- Only compaction moves an epoch, and only upwards (`$inc epoch` in
  `mongo.CompactChangeInfos`). Housekeeping sweeps `FindCompactionCandidates`
  across every project in the database each interval, and the integration
  package shares one `test-yorkie-meta-*` database between servers, so the
  sweep reaches a document a test has just left detached.
- `AttachDocument` seeds `ClientDocInfo.Epoch` from the `docInfo` the handler
  got from `FindOrCreateDocInfo`, which reads MongoDB directly and neither
  reads nor writes `docCache`. `pushPack` and `preparePack` compare that seed
  against `FindDocInfoByRefKey`, which is served from `docCache`.
- `CompactChangeInfos` calls `docCache.Remove` *before* its conditional
  `UpdateOne`, so a read landing in that window re-adds the pre-compaction
  `DocInfo` and leaves the cache an epoch behind the row indefinitely. The
  next fresh attach then seeds the new epoch from the row and immediately
  mismatches the stale cached one.

Nothing on this branch touches compaction, `docCache` or epochs, and the
branch's previous CI run passed on the same `pushpull.go` code — the only
diff since was comments and docs. The repair belongs in
`server/backend/database/mongo/client.go` (claim the document first, then
invalidate, or have `FindOrCreateDocInfo` and `FindDocInfoByRefKey` agree on
one source), which is outside this change's files.

## Review round 2 (blast radius, design fit, security)

Four blocking findings, all about the rejection rather than the compare. They
were taken to mean the rejection had to go, and the first fix replaced it with
a log:

- A rejection is an un-negotiated wire precondition for initial-actor change
  IDs, and lands with no staged rollout for a writer population that lives in
  another repository.
- The identity it authorizes on is not authenticated, so #2120 stays reachable
  with the gate in place.
- Operation tickets were unchecked.

## Review round 3 (security, test adequacy)

Dropping the rejection went too far, and the two findings of this round are
the same mistake seen from either end:

- The "it buys no security" step conflated two identities. It is true of a
  caller holding a victim's `client_id`; it is false of the collaborator that
  only read the victim's actor off the wire, which is the reachable attack and
  which the compare does refuse. See "What the compare is worth" above. The
  Watch path enforcing the identical compare was the tell that the push path
  should too.
- With the rejection gone, nothing in the diff was testable: the tests
  asserted that pushes succeed, which was already true on `main`, so deleting
  the diagnostic or inverting its predicate left them green.

The fix keeps the two carve-outs the round-2 findings were right about — the
initial actor and operation `executedAt` tickets, neither of which is part of
the attack — and restores `ErrActorMismatch` for a foreign actor in a change
ID. Compatibility rests on the writer survey above, not on an assumption: both
SDKs stamp an `IsOwnActor` actor into every change ID before the pack is built,
and the server's writers hold the initial actor as their session id.

Still open and out of these files: a caller holding a victim's identifier, and
sibling sessions of one client key, need an authenticated client identity
(#2114), which touches the auth and RPC layers. The pushed version vector's
actors are still unexamined.

## Self-review (round 1, correctness/tests/compatibility)

Done by the implementing agent over the full branch diff; no separate
reviewer was launched.

- Compatibility: covered above; no honest SDK or server path stamps an actor
  outside `IsOwnActor`. Not blocking.
- Epoch mismatch: a forged change in a stale-epoch pack now fails with
  `ErrActorMismatch` instead of `ErrEpochMismatch`. Both refuse the pack, and
  an honest pack still gets `ErrEpochMismatch`. Not blocking.
- The error is `clients.ErrActorMismatch` (PermissionDenied), the one `Watch`
  returns, so SDKs see one code for one condition. `packs` importing
  `clients` adds no cycle (`clients` imports only backend, database,
  messaging and logging).
- Out of scope, listed in the PR as known limitations: operation tickets are
  not checked; the pushed version vector may still name other actors; the
  client key is not a credential, so a client that activates with the
  victim's key derives the victim's stable actor (#2114).

No blocking findings; stopped after round 1.

## What the check does not establish

The identity the check binds to is unauthenticated, and that bounds what it
can claim:

- `clientInfo` comes from the request's `client_id`, which is not a
  credential. A caller holding a victim's `client_id` is the victim as far as
  every RPC is concerned, and the compare passes trivially.
- `StableActorID = DeriveActorID(project_id, client_key)` is deterministic
  with no unique index (`memory/database.go` `ActivateClient`), so a second
  activation of the same key takes a new session id and the *same* actor.
  A caller holding a victim's client key therefore passes the check for the
  victim's actor, and two honest sessions of one key are indistinguishable —
  one session's change still lands in the other's pull dedup.

So the compare refuses the collaborator that only holds a victim's actor, but
not a caller that holds one of its identifiers; the rest of #2120 needs an
authenticated client identity, which is #2114 and touches the auth and RPC
layers rather than `packs`. The shared-key case is pinned by a test in
`server/packs/push_actor_test.go` so the limit is recorded in the suite rather
than only in prose.

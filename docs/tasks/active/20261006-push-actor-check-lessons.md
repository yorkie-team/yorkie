**Created**: 2026-10-06

# Reject Pushed Changes Stamped With Another Client's Actor — Lessons

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
cluster detach and the server writers at once. As with an invalid clientSeq,
an attach that fails here leaves the attaching row the handler already wrote;
the handler's deferred invalidation covers it.

Only changes above the checkpoint are checked, because only those are
stored; `pushPack` drops the rest before they reach the database or the
publisher. A test pins that an already-acked change with another actor does
not fail the pack.

## Proving the tests

With the check disabled, `TestPushActorCheck` in `server/packs` fails on every
forged case, and the integration test shows the bug itself: the forged
`{"x":1}` is stored, the honest retry with the same clientSeq is skipped as
already pushed, and the victim stays at `{}` after a sync.

The integration test first re-attached a fresh document under the same actor.
That reproduces the pre-attach re-issue design's known gap (the pull filter
skips the actor's own earlier changes), not this change, so the test uses a
reactivation, which takes a new session id, instead.

## Environment

Another agent's integration run held ports 11101/11201 for a while, and
`make verify` failed in `TestWatchAccessRevalidation` on "address already in
use". Re-running once the ports were free passed; it was not this change.

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

So the check hardens the push path (a client cannot stamp an actor it holds no
identifier for, including the initial actor) but does not close #2120 on its
own; that needs an authenticated client identity, which is #2114 and touches
the auth and RPC layers rather than `packs`. The claim was scoped to this in
`pushpull.go`, both design docs and the todo, and the shared-key case is
pinned by a test in `server/packs/push_actor_test.go` so it reads as a known
limit rather than a guarantee.

**Created**: 2026-10-06

# Refuse Pushed Changes Stamped With Another Client's Actor

**Goal:** the push path stored a change with whatever actor its ID carried,
and the pull path drops a change whose actor `IsOwnActor` matches the pulling
client (`pullChangeInfos`), taking it for the client's own echo. A client that
pushes a change stamped with a victim's actor therefore makes the victim skip
it while every other peer applies it, keyed on the victim's presence identity,
and the victim never converges (#2120). Refuse that change on push.

Scope: the compare is against the client row the request names in `client_id`,
the same compare the Watch path already enforces (`yorkie_server.go` Watch,
`ErrActorMismatch`). What it buys: a collaborator reads a peer's actor off
every change it pulls, but `StableActorID` is a digest of (project, client key)
with no preimage, so the actor is the only identity of that peer it holds —
and stamping it is now refused. What it does not buy: `client_id` is not a
credential, so a caller already holding a victim's identifier resolves a row
for which the victim's actor is its own, and two sessions of one client key
share a stable actor by design. Both need an authenticated client identity,
which is #2114. The initial actor stays accepted: it is the documented value of
a pre-attach edit in a client that skipped `SetActor`, of the tickets a
declined re-issue leaves behind and of the server's own writers, and it is no
client's own actor on pull, so a change stamped with it suppresses nobody.

## Tasks

- [x] Check every legitimate path that pushes a change for an actor other
      than the session id or the stable actor:
      - Go client: `Attach` stamps `loadID()` (session id) with
        `SetActorWithOptions` before the pack is built; `SetActor` rewrites
        every local change ID, with or without the re-issue.
      - JS SDK: `attach`/`remove` stamp `actorID ?? id` before
        `createChangePack`; `restoreFromBytes` refuses a persisted changeID
        whose actor differs from the stamped one.
      - Server writers: revision restore and document update push as
        `SystemClientInfo` (ID = `InitialActorID`) with `InitialActorID`
        changes, which `IsOwnActor` accepts as their session id; cluster
        `DetachDocument` stamps the session id.
- [x] `validateChangeActors` in `server/packs`, run in `PushPull` step 00 next
      to the clientSeq check: for every change the server would store
      (clientSeq above the checkpoint), refuse with `ErrActorMismatch` when the
      change ID actor is one `IsOwnActor` rejects. The initial actor is exempt
      (see Scope). An operation's `executedAt` actor is logged, not refused: it
      keeps the actor it was minted under, which a declined re-issue leaves as
      the initial actor, and neither the pull dedup nor the presence keying
      reads it.
- [x] Unit tests in `server/packs` (`push_actor_internal_test.go`, no build
      tag): own session id, own stable actor, the initial actor, the server
      client and an already-acknowledged change pass; another client's actor in
      a change ID is refused, in an operation ticket is not.
- [x] Integration tests in `server/packs`: over the wire, another client's
      session id and stable actor are refused with `ErrActorMismatch` while the
      initial actor and the pusher's own identities land, on push and on
      attach; same-key sessions share one actor, the documented limit.
- [x] Integration test: the #2120 forgery is refused and the honest Go flows
      (pre-attach edits, push/pull, detach + re-attach, reactivate with the
      same key) still converge.
- [x] `make verify`; integration tests with MongoDB up.
- [x] Self-review; log it in the lessons file.

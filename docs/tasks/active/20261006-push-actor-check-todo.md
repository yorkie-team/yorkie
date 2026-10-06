**Created**: 2026-10-06

# Reject Pushed Changes Stamped With Another Client's Actor

**Goal:** the push path stores a change with whatever actor its ID carries,
and the pull path drops a change whose actor `IsOwnActor` matches the pulling
client (`pullChangeInfos`), taking it for the client's own echo. A client that
pushes a change stamped with a victim's actor therefore makes the victim skip
it, and the victim never converges (#2120). `Watch` already refuses a declared
actor that is not the client's own (`ErrActorMismatch`). Refuse such a push the
same way.

Scope: the change ID actor only, bound to the client row the request names in
`client_id`. Tickets inside operations are not checked; old SDKs carry
pre-attach tickets under the initial actor.

This hardens the push path but does not close #2120. `client_id` and the
client key are not credentials, and `StableActorID` is derived
deterministically from (project, client key) with no unique index, so a caller
holding a victim's `client_id` or client key resolves a client row for which
the victim's actor is its own and passes the check; two honest sessions of one
client key do too, and the pull dedup can still drop a sibling session's
change. Binding a push to an authenticated identity is #2114.

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
        changes; cluster `DetachDocument` stamps the session id.
- [x] `validateChangeActors` in `server/packs`, run in `PushPull` step 00
      next to the clientSeq check, before anything is written: every change
      the server would store (clientSeq above the checkpoint) must carry an
      actor `IsOwnActor` accepts, else `clients.ErrActorMismatch`.
- [x] Tests in `server/packs`: another client's session id, another client's
      stable actor and the initial actor are refused on push and on attach,
      leaving the document and checkpoint untouched; the client's own session
      id and stable actor are accepted.
- [x] Integration test: the forged change no longer hides from the victim;
      honest Go flows (pre-attach edits, push/pull, detach + re-attach,
      reactivate with the same key) still converge.
- [x] `make verify`; integration tests with MongoDB up.
- [x] Self-review; log it in the lessons file.

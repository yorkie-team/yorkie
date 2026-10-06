**Created**: 2026-10-06

# Log Pushed Changes Stamped With Another Client's Actor

**Goal:** the push path stores a change with whatever actor its ID carries,
and the pull path drops a change whose actor `IsOwnActor` matches the pulling
client (`pullChangeInfos`), taking it for the client's own echo. A client that
pushes a change stamped with a victim's actor therefore makes the victim skip
it, and the victim never converges (#2120). Make that condition visible on the
push path.

Scope: a log line, not a rejection. The only identity a push-side compare can
reach is the client row the request names in `client_id`, and neither
`client_id` nor the client key behind `StableActorID` is a credential —
`StableActorID` is derived deterministically from (project, client key) with no
unique index, so a caller holding a victim's `client_id` or client key resolves
a client row for which the victim's actor is its own, and two honest sessions of
one key share one actor by design. Rejecting on that compare would stop no
attacker who holds an identifier, while refusing pushes the server has always
accepted from SDK versions this repository cannot enumerate (a client that
skips `SetActor` sends pre-attach changes under the initial actor). Enforcement
belongs with an authenticated client identity, which is #2114.

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
- [x] `logForeignActors` in `server/packs`, run in `PushPull` step 00 next to
      the clientSeq check: for every change the server would store (clientSeq
      above the checkpoint), warn when the change ID actor or an operation's
      `executedAt` actor is one `IsOwnActor` rejects. The initial actor is not
      reported — it is the documented value of a pre-attach edit, of the
      tickets a declined re-issue leaves and of the server's own writers.
- [x] Tests in `server/packs`: another client's session id, another client's
      stable actor and the initial actor all land on push and on attach, so no
      new wire precondition is introduced; same-key sessions share one actor,
      which is why the compare cannot authorize.
- [x] Integration test: honest Go flows (pre-attach edits, push/pull,
      detach + re-attach, reactivate with the same key) still converge. The
      #2120 forgery is kept as a skipped reproducer until #2114.
- [x] `make verify`; integration tests with MongoDB up.
- [x] Self-review; log it in the lessons file.

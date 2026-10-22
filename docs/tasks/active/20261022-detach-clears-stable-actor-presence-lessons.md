# Lessons — detach clears a stable-actor client's presence

**Created**: 2026-10-22

## Notes

- `ClientInfo` carries two identities and they are not interchangeable:
  `ID` keys the client *row* (per session, used by `ClientRefKey`), while
  `OwnActorID()` is the actor the client *stamps into changes*. Any server-side
  code that writes a change on a client's behalf, or that looks a change up by
  actor, must use the latter. `IsOwnActor` is the read-side counterpart.
- `FindLatestChangeInfoByActor` is not consistent across backends on a miss:
  Mongo returns a zero-valued `ChangeInfo` with a nil error, the memory DB
  returns `database.ErrChangeNotFound`. Callers must handle both; the cluster
  detach previously did not, which would have turned a miss into a failed
  deactivation on the memory backend.

- The issue's second bullet — "the clear change is stamped with lamport 0 and
  an empty version vector" — turns out to be cosmetic, and true of *every*
  presence-only change rather than only the server-written one.
  `change.Context.ToChange` takes the `IsPresenceOnlyChange` branch and rebuilds
  the ID as `prevID.Next(true)`, which carries only clientSeq and actor and
  drops lamport and the version vector. So whatever
  `FindLatestChangeInfoByActor` returns is discarded before the change is
  stored. The lookup was still keyed on the right actor here, because a wrong
  key is a latent trap and because the memory backend turns a miss into an
  error (below); the fix that matters is the actor stamped into the change ID.
- `pushPack` drops a change whose `clientSeq <= cpBeforePush.ClientSeq`, and
  `DetachDocument` builds its context from `cp.ClientSeq`. It survives only
  because `ToChange`'s presence-only branch bumps the clientSeq by one. Worth
  knowing before anyone "simplifies" either side.

## Review rounds

- `/self-review` was not run: this change was produced by an autonomous,
  single-shot run that is granted no tool able to launch the reviewer subagent.
  Recording it here so the skipped round is not mistaken for a clean one. The
  PR's reviewers are CI, `@claude review`, and a human maintainer.

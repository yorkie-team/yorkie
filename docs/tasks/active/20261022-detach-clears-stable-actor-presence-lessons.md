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

## Review rounds

- `/self-review` was not run: this change was produced by an autonomous,
  single-shot run that is granted no tool able to launch the reviewer subagent.
  Recording it here so the skipped round is not mistaken for a clean one. The
  PR's reviewers are CI, `@claude review`, and a human maintainer.

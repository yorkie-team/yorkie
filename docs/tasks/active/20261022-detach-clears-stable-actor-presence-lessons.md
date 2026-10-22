# Lessons — detach clears a stable-actor client's presence

**Created**: 2026-10-22

## Notes

- `ClientInfo` carries two identities and they are not interchangeable:
  `ID` keys the client *row* (per session, used by `ClientRefKey`), while
  `StableActorID` is derived from (project, client key).
- `OwnActorID()` reads as "the actor this client stamps into its changes", but
  it cannot be that: `ActivateClient` derives a `StableActorID` for every row,
  and the response carries *both* identities, leaving the choice to the SDK.
  The JS client stamps `actor_id`, the Go client stamps `client_id`
  (`client.Activate` stores `response.Msg.ClientId`). So `OwnActorID()` is a
  guess that is right for one SDK generation and wrong for the other — the
  first attempt at this fix keyed the clear on it and simply moved the leak
  from the JS SDK to the Go SDK, which is what the integration test caught.
  `IsOwnActor` is the honest read-side counterpart: it accepts either. A
  server-side write on a client's behalf has to cover both, which for an
  idempotent presence clear just means emitting one per identity — a clear
  stamped with an identity holding no presence deletes nothing and reconciles
  to no event (`InternalDocument.applyChanges`).
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
  stored; both clears therefore share one lookup. It is kept because the memory
  backend turns a miss into an error (above) and because the presence-only
  branch is not a contract — the fix that matters is the actor stamped into the
  change ID.
- `pushPack` drops a change whose `clientSeq <= cpBeforePush.ClientSeq`, and
  `DetachDocument` builds its context from `cp.ClientSeq`. It survives only
  because `ToChange`'s presence-only branch bumps the clientSeq by one. Worth
  knowing before anyone "simplifies" either side; the second clear change is
  built from `cp.ClientSeq+1` for the same reason, and
  `validateClientSeqContinuity` wants exactly that +1/+2 pair.
- Presence-only changes never reach the Mongo `changes` collection — they live
  in the `presenceCache` `ChangeStore` only (`CreateChangeInfos` step 03). So
  `FindLatestChangeInfoByActor`, which queries that collection, cannot be used
  to infer which actor a presence-only client has been stamping.

## Review rounds

- `/self-review` was not run: this change was produced by an autonomous,
  single-shot run that is granted no tool able to launch the reviewer subagent.
  Recording it here so the skipped round is not mistaken for a clean one. The
  PR's reviewers are CI, `@claude review`, and a human maintainer.
- CI stood in for the missing round: the integration lane failed on the first
  attempt, and so did `RunDeactivateClientWithAttachedDocumentTest`, which
  asserts that a detach writes a clear change under the session ID. Both were
  the same mistaken premise about which actor a client stamps. The second
  integration subtest, which drives the stable-actor identity over raw RPC,
  exists so the symmetric mistake fails locally next time.

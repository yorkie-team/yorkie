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
  stored. The lookup was kept at first and removed in a later round (below) —
  the fix that matters is the actor stamped into the change ID.
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
- Review round (panel): the stable actor is `DeriveActorID(project_id,
  client_key)`, so every session of one client key resolves the same actor and
  the sessions share one presence entry keyed under it. An unconditional
  stable-actor clear on a per-session detach therefore deletes presence a live
  sibling session still owns, and de-onlines it on every peer. The clear is now
  gated on `FindAttachedClientInfosByRefKey`: it is skipped while another
  activated row attached to the same document carries the same
  `StableActorID`. The session-ID clear needs no gate — that identity is the
  row's alone.
- The same sharing made the `presenceCache` prune by `StableActorID` unsafe:
  `ChangeStore.RemoveChangesByActor` deletes from a range the store still
  counts as fetched, so a sibling's presence changes would be dropped and never
  refetched. The prune is an optimization — it keeps Clear changes, so the
  replayed state is cleared either way — so it stays keyed on the session ID.
- `FindLatestChangeInfoByActor` is gone from `DetachDocument`. Its result was
  discarded by `ToChange`'s presence-only branch (noted above), so the lookup
  bought a DB round-trip and a backend-disagreement workaround for no effect on
  the stored change. Giving a presence-only clear real clocks would need
  `pkg/document/change`, and would write a version-vector liveness entry back
  under an actor that is leaving.
- Known limitation of that gate: `FindAttachedClientInfosByRefKey` reports only
  rows whose `ClientDocInfo.Status` is already `attached`. A sibling session
  whose attach is still in flight is in the `attaching` state for the whole
  `AttachDocument` RPC — `TryAttaching` stamps it up front,
  `UpdateClientInfoAfterPushPull` flips it at the end of `PushPull` — yet the
  presence that attach pushes is stored before the flip. So the gate can miss a
  sibling that already owns the shared entry, and the stable-actor clear still
  deletes it. The window is the reload/offline-resume flow, not a rare race.
  Closing it needs a query over attaching sessions (the `database.Database`
  interface and the memory backend, neither in this branch's diff) or an
  attach/detach-exclusive document lock (`yorkie_server.AttachDocument` takes
  `DocAttachmentKey` only under an attachment limit, a `RemoveOnDetach` project
  or a schema rebind, so it is not a lock this handler can rely on). Both are
  follow-up work, tracked as #2294; `hasAttachedSibling` carries the same note.
- Maintainer handoff: the loop stopped after three fix rounds. The remaining
  blocking findings were settled by hand — the "redundant clear is inert"
  claim is pinned by `TestRedundantPresenceClear` in `pkg/document` (a
  deterministic unit test, not a watch-stream timing test), and the attaching
  blind spot went to #2294.

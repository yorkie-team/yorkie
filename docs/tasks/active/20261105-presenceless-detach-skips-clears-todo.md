# Skip the presence clears of a server-side detach on presenceless documents

**Created**: 2026-11-05

## Why

Since #2291, every server-side detach (`clients.Deactivate` →
`clusterServer.DetachDocument`, also driven by housekeeping) writes presence
clears under the session ID and, unless a sibling session of the same client
key is attached, under the stable actor. Deciding the second one calls
`hasAttachedSibling`, which reads every activated client attached to the
document through `FindAttachedClientInfosByRefKey` and compares
`StableActorID` in Go. The check runs on every detach, because
`ActivateClient` sets `StableActorID` on every row.

On a document created with `disable_presence` all of that is wasted work:
`PushPull` strips every presence-only change on the way in
(`stripPresenceChanges`), so both clears are dropped before they reach the
changes collection. The sibling read is the expensive part. In one production
project a daily presenceless document ends the day with 88,385 activated
clients attached — clients that left without a successful deactivate — and
each server-side detach of it reads all of them: 88,385 keys and documents on
the shard (420 ms), 38.5 MB over the wire (~1.4 s end to end) and ~157 MB of
allocations to decode, at 1–9 detaches/s (13.5/s peak), all on the document's
owner node and one shard.

## Plan

- [x] Read `DocInfo` before building the pack in `clusterServer.DetachDocument`
      and, when `DisablePresence` is set, build no clears and skip
      `hasAttachedSibling`. The detach itself (status, checkpoint, removal)
      is unchanged
- [x] Integration test in `server/rpc`: a server-side detach of a presenceless
      document does not read the attached clients, and the client still ends
      up detached and deactivated; the same flow on a document with presence
      reads them once (shows the probe works)
- [x] `docs/design/disable-presence.md`: add the server-side detach to the
      synchronization rules
- [x] `make verify`, `make test` (full integration suite with
      `-race`)
- [x] Measure before/after with a presenceless document carrying ~88k
      attached clients
- [x] Self review (round 1, no blocking findings; see lessons)
- [ ] PR, then archive the task pair before merge

## Out of scope

- Narrowing the sibling check itself for documents with presence (an
  existence query on `stable_actor_id`, with an index) — tracked with the
  correctness issues of the same guard in #2294.

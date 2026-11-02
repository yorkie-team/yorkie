# Server-side detach must clear a stable-actor client's presence

**Created**: 2026-10-22

Issue: yorkie-team/yorkie#2290

## Problem

When the server detaches a client on its behalf (`clients.Deactivate`, which
housekeeping also drives for clients that left without detaching), the
presence-clear change it writes is stamped with the client's *per-session* ID
(`ClientInfo.ID`). The JS SDK records presence under the `StableActorID` that
the same `ActivateClient` response carries, so `presences.Delete(actorID)`
finds no entry and the departed client's presence survives forever: it lands in
every snapshot (`AllPresences`) and every peer keeps it in memory.

The server cannot tell which of the two identities a given client stamps:
`ActivateClient` derives a `StableActorID` for *every* client row, and the SDKs
choose for themselves — the Go client stores `client_id` and stamps that, the
JS client stamps `actor_id`. So `OwnActorID()` is not an answer either; keying
the clear on it alone just moves the leak from new SDKs to old ones.

Two side effects of the same mismatch:

- `FindLatestChangeInfoByActor` is called with the session ID, so it finds
  nothing and the clear change is stamped with lamport 0 and an empty version
  vector.
- `RemoveChangesByActor(info.ID)` in the Mongo client's presence cache uses the
  session ID too, so the cached presence changes are not dropped either.

## Plan

As shipped, after three review rounds:

1. `server/rpc/cluster_server.go` `DetachDocument`: keep the session ID for
   looking up the client row (that is its key), and write one clear change per
   identity the client may have stamped — the session ID always, and
   `clientInfo.OwnActorID()` when it differs. A clear under an identity that
   holds no presence deletes nothing and reconciles to no event, so the
   redundant one is inert. The session ID goes first, keeping the clientSeq a
   single clear took.
2. Skip the stable-actor clear while another session of the same client key is
   still attached to the document (`hasAttachedSibling`): the stable actor is
   derived from (project, client key), so those sessions share one presence
   entry.
3. Drop the `FindLatestChangeInfoByActor` lookup. `Context.ToChange` discards
   the lamport and version vector of every presence-only change, so its result
   never reached the stored change, and the memory backend turned a miss into
   a failed deactivation.
4. Keep the presence-cache prune in `UpdateClientInfoAfterPushPull` on the
   session ID. Pruning the stable actor too was tried and reverted: it would
   drop a sibling session's cached changes.
5. Tests:
   - unit: `TestRedundantPresenceClear` (`pkg/document`) — a clear pair as the
     server writes it publishes exactly one unwatched event, in either order.
   - integration: `TestDeactivateClearsPresence`, one subtest per identity and
     one for a still-attached sibling session.
6. Record the new site in `docs/design/offline-resumable-attach.md` Seam 2.

## Follow-up

- #2294 — the sibling guard does not see a session that is still attaching,
  so its shared stable-actor presence can still be cleared.

## Non-goals

- Changing the wire protocol or the generated protobuf.
- Changing how clients choose between session and stable actor.

## Acceptance criteria

- A server-side detach clears the presence the client actually wrote, for old
  SDKs (session ID) and new ones (stable actor) alike.
- After deactivation the document, its snapshots and every peer no longer hold
  the departed client's presence.

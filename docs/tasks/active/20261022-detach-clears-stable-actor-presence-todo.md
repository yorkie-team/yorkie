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

1. `server/rpc/cluster_server.go` `DetachDocument`: keep the session ID for
   looking up the client row (that is its key), and write one clear change per
   identity the client may have stamped — the session ID and, when it differs,
   `clientInfo.OwnActorID()`. A clear under an identity that holds no presence
   deletes nothing and reconciles to no event, so the redundant one is inert.
   The session ID goes first, keeping the clientSeq a single clear took.
2. Tolerate `database.ErrChangeNotFound` from `FindLatestChangeInfoByActor`:
   the memory DB returns it where Mongo returns a zero `ChangeInfo`, and a
   client that attached without writing a change has no latest change under
   either identity. Treat it as "no prior change" rather than failing the
   detach.
3. `server/backend/database/mongo/client.go`
   `UpdateClientInfoAfterPushPull`: drop cached presence changes under both
   identities the client may have stamped, mirroring `ClientInfo.IsOwnActor`.
4. Tests:
   - unit: `ChangeStore.RemoveChangesByActor` already covered.
   - integration, one subtest per identity: a Go client attaches with a
     presence, deactivates without detaching, and neither the peer nor a client
     attaching afterwards sees leftover presence; and the same over raw RPC for
     a presence put under the stable actor, which the Go client never stamps.

## Non-goals

- Changing the wire protocol or the generated protobuf.
- Changing how clients choose between session and stable actor.

## Acceptance criteria

- A server-side detach clears the presence the client actually wrote, for old
  SDKs (session ID) and new ones (stable actor) alike.
- After deactivation the document, its snapshots and every peer no longer hold
  the departed client's presence.

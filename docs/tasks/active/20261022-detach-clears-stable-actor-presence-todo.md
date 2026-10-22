# Server-side detach must clear a stable-actor client's presence

**Created**: 2026-10-22

Issue: yorkie-team/yorkie#2290

## Problem

When the server detaches a client on its behalf (`clients.Deactivate`, which
housekeeping also drives for clients that left without detaching), the
presence-clear change it writes is stamped with the client's *per-session* ID
(`ClientInfo.ID`). Clients on current SDKs record presence under their
`StableActorID` (`ClientInfo.OwnActorID()`), so `presences.Delete(actorID)`
finds no entry and the departed client's presence survives forever: it lands in
every snapshot (`AllPresences`) and every peer keeps it in memory.

Two side effects of the same mismatch:

- `FindLatestChangeInfoByActor` is called with the session ID, so it finds
  nothing and the clear change is stamped with lamport 0 and an empty version
  vector.
- `RemoveChangesByActor(info.ID)` in the Mongo client's presence cache uses the
  session ID too, so the cached presence changes are not dropped either.

## Plan

1. `server/rpc/cluster_server.go` `DetachDocument`: keep the session ID for
   looking up the client row (that is its key), but stamp the clear change — and
   run the latest-change lookup — with `clientInfo.OwnActorID()`.
2. Tolerate `database.ErrChangeNotFound` from `FindLatestChangeInfoByActor`:
   the memory DB returns it where Mongo returns a zero `ChangeInfo`, and a
   client that attached without writing a change has no latest change under
   either identity. Treat it as "no prior change" rather than failing the
   detach.
3. `server/backend/database/mongo/client.go`
   `UpdateClientInfoAfterPushPull`: drop cached presence changes under both
   identities the client may have stamped, mirroring `ClientInfo.IsOwnActor`.
4. Tests:
   - unit: `ChangeStore.RemoveChangesByActor` already covered; add an internal
     test for the actor the cluster detach stamps where one is cheap.
   - integration: a client attaches with a presence, deactivates without
     detaching, and a second client attaching afterwards sees no leftover
     presence.

## Non-goals

- Changing the wire protocol or the generated protobuf.
- Changing how clients choose between session and stable actor.

## Acceptance criteria

- A server-side detach clears the presence the client actually wrote, for old
  SDKs (session ID) and new ones (stable actor) alike.
- After deactivation the document, its snapshots and every peer no longer hold
  the departed client's presence.

# Lessons: Bound the presence a client may push as a put

## Notes

- `converter.FromPresenceChange`, where the finding pointed, is the decode
  path for stored rows as well as for the wire
  (`server/backend/database/change_info.go` calls it when reading a
  `ChangeInfo` back). A size refusal there would turn a row stored before the
  bound into a read error on every pull, so the gate belongs on the push path
  instead, where it can measure only what is about to be stored.
- The bound cannot be `maxPresenceBaseSize`. A presence over 4 KiB is already
  a supported case: it is simply never cached nor folded into, and the client
  pushes full puts. Reusing that constant as a hard refusal would reject
  clients that work today, including the ~7.4 KB presence that motivated the
  patch work in the first place.
- Only the changes above the client's checkpoint are measured, which is the
  set `pushPack` stores. Measuring the whole pack would refuse a reconnecting
  client that resends an already-acked change whose presence the server
  accepted before this bound existed.
- The bound cannot refuse the push. A client keeps a local change until the
  server acknowledges it (`InternalDocument.RemovePushedLocalChanges`), so a
  pack the server never stores is pushed again unchanged: a refusal is
  permanent rather than something a smaller presence clears, it stalls the
  document's operations along with its presence, and it blocks the detach and
  remove packs that every other write gate on this path deliberately lets
  through. Dropping the presence and keeping the push is the answer the fold
  already gives a patch it cannot pay for.
- Dropping an oversized put has to drop the cached base with it. `commit`
  rebuilds the base from the puts that were actually stored, so leaving the
  entry alone would let the client's next patch fold onto the presence from
  before the dropped put — a presence the sender no longer holds.
- A per-put bound alone is not a bound. Only clientSeq continuity limits how
  many changes a pack carries, so the same amplification is available to a
  pack of a few hundred puts at the bound; the fold budget already had this
  shape for server-produced puts and the pushed ones need their own.

- "The push is kept" is not the same as "the change is stored". Stripping the
  presence off a presence-only change leaves it carrying nothing, and
  `CreateChangeInfos` persists neither branch of its split: `PresenceOnly()`
  is false once the presence is gone and `HasOperations()` was never true, so
  the change is silently dropped on the MongoDB path (the memory DB inserts
  it, so the two backends differ here). What keeps the client from wedging is
  not the row but the checkpoint: `SyncClientSeq` runs over every change the
  store is handed, dropped ones included, so the push is acknowledged. An
  integration test asserting the peer pulls an operation-less change was
  asserting the backend's behaviour, not the guarantee; it now asserts the
  guarantee directly — the client's next in-bound put is accepted on the
  following clientSeq, and the oversized presence never reaches a peer.

- Dropping an oversized put is the only drop on this path that discards a
  client's *final* presence. Every other one has a presence behind it: a
  coalesced run keeps the put of its last change, and a patch is dropped for a
  missing base only when a later put or clear in the same pack replaces the
  presence anyway. So the sender is the replica left out of step rather than
  its peers, and it converges only at its next snapshot pull —
  `InternalDocument.applySnapshot` replaces the whole presence map, and the
  dropped put was acknowledged, so it is not among the local changes replayed
  afterwards. The rollback is silent; surfacing it needs a field on the push
  reply, which is the SDK change the todo rules out of scope.

## Self review

Not run: this branch was produced by the autonomous agent workflow, which is
granted no tool that can launch the reviewer subagent. CI, `@claude review`
and a human reviewer stand in for it on the PR.

**Created**: 2026-09-27

# Mark LWW losers removed and stop leaking GC size records

Go side of yorkie-team/yorkie-js-sdk#1398 and item 2-3 of #1395. A Go/JS
parity audit found two docSize/GC accounting gaps the JS SDK has already
closed.

## Problems

1. **G1: an LWW loser is only marked removed when the occupant is live.**
   `ElementRHT.SetWithExecutedAt` gates the losing branch on
   `!node.isRemoved()` -- the occupant's state -- where JS gates it on the
   incoming value's own state.
   - A live value that loses to a tombstoned occupant stays live in
     `nodeMapByCreatedAt`: `Nodes()` emits it, it is never registered for GC
     and it stays charged to Live. The replica that saw the tombstone first
     disagrees on docSize and GarbageLen with the one that saw the loser
     first; the server's replay is one of those replicas.
   - A loser that is already removed (a decoded tombstone) can have its
     removedAt bumped to the occupant's ticket, because `Remove` accepts a
     later ticket.
2. **G2: `Root.release` leaks its `sizeInGC` record.** It writes a zero
   record for a tombstone the restore orphaned and drops its collection
   entry, so the record is never deleted -- only `deregisterElement` deletes
   records, and it never runs for a released element. The map is keyed by
   pointer, so each remove/undo cycle also pins a dead subtree. JS moved the
   map to a `WeakMap`.

## Plan

- [x] Red: `TestElementRHTSetLoser` (crdt, element RHT) and a
      document-level test delivering the same two changes to two replicas in
      opposite orders.
- [x] Green: gate the losing branch on `v.RemovedAt() == nil`; re-check the
      order-independence comment on `Nodes()`.
- [x] Red: internal crdt test cycling remove -> restore -> collect and
      asserting `sizeInGC` stays bounded and docSize matches a rebuild.
- [x] Green: retire a released element's zero record when `elementMap`
      stops answering with it (the restored copy takes the slot over).
- [x] `make verify` per commit; `make test` (MongoDB was up) green.
- [ ] Self review (max 3 rounds), log in lessons.
- [ ] Rebase on `origin/main`, push, open PR.

## Design (G2)

The zero record exists because a released subtree stays addressable: a peer
that has not seen the restore can still remove a member inside it, and
without a record `moveSizeToGC` would take that member's size out of Live a
second time. The record is needed exactly as long as the element is
addressable. In Go the only index that makes an element addressable is
`elementMap`, and the restore that orphans the subtree re-registers a copy
under the same createdAts right after `release`. So `registerLive` drops a
zero record of the element whose slot it takes over. A member only the
tombstone had (a peer's concurrent add) keeps its slot and its record, which
is what the protection is for.

A weak-keyed map (Go's `weak` package) was considered and rejected: it needs
a concrete pointer type while the key is the `Element` interface, and it
would make the lifetime depend on the Go GC rather than on document state.

## Review

- Red: `TestElementRHTSetLoser` (loser stayed live; removedAt 2 -> 3),
  `TestSetLoserAgainstTombstoneConverges` (GarbageLen 3 vs 4, Live Data 6 vs
  2), `TestReleasedSizeInGCRecordIsRetired` (sizeInGC 2 after one cycle, 102
  after 51).
- Green: all three pass; `make verify` and `make test` green.

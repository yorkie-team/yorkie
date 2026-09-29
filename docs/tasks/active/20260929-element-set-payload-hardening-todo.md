**Created**: 2026-09-29

# Harden Set against payloads no replica can produce

Follow-up to #2069. Six review rounds on that PR grew defenses against
crafted and duplicated payloads on top of its two parity fixes. They moved
here so #2069 could land as the two fixes alone.

## Problem

`createdAt`, `movedAt` and `removedAt` of an element payload are decoded from
client bytes independently of the operation's `executedAt`. A Set whose value
cannot be tombstoned, or whose createdAt collides with a live member, used to
stay live, unreachable by key and charged to Live, or take over a live
member's slot in `Root.elementMap`.

## Carried over from #2069

- [x] Refuse an LWW loser that cannot be tombstoned (`SetWithExecutedAt`
      returns `indexed`).
- [x] Skip the Root bookkeeping for a refused Set, at every call site
      (`operations.Set`, `json.Object`, the element decoder).
- [x] Guard the document-wide createdAt slot on every Set.
- [x] Validate the ticket triple at the converter boundary and rescue the new
      rejections on the stored-operation path.
- [x] Register the CRDT container, not the json proxy, as the recorded parent.

## Round 6 blockers

- [x] ArraySet redo re-inserts a value a peer removed, with a removedAt older
      than its new createdAt. Red: `TestArraySetRedoAfterPeerRemovalKeepsTheTombstoneOut`.
      Fix: build a Remove reverse when the displaced value is a tombstone, as
      `Set.Execute` does.
- [x] Dropping the movedAt rule lets a crafted member be evicted without
      being tombstoned. Two ways in: a member payload with movedAt before
      createdAt, and a Set whose executedAt precedes its value (the win
      stamps movedAt = executedAt). Fix: reject an object member positioned
      before its createdAt, keeping the exemption for array elements that
      undo re-identifies, and reject a Set value created after its Set.
- [x] The snapshot decode path has no rescue for `ErrRefusedMember`. Red:
      `TestRefusedMemberInSnapshotStaysLoadable`. Fix: the container decoders
      take `dropRefused`; a snapshot drops the member (no key reached it, so
      the content is unchanged), a pushed payload still rejects it.

## Code review

- [x] The JS SDK's ArraySet reverse still re-inserts a removed value with a
      newer createdAt, so the removedAt rule refused pushes from current JS
      clients, and documents already hold that shape nested. Fix: the ticket
      rules apply to object members and the Set value only; array elements
      are exempt.
- [x] The stored path dropped operations that had applied on every replica
      before a rule existed, diverging the server's replay and breaking any
      later operation that referred to them. Fix: the rules and the refused
      member error move to the push boundary (`FromPushedChangePack`,
      `ValidatePushedOperations`); every other reader decodes leniently, and
      `normalize.go` is back to `main`.
- [x] The createdAt-slot refusal also fired on a tombstone, so a losing undo
      restore left C's older removedAt on one replica. Red:
      `TestLosingUndoRestoreConverges` (green on `main`). Fix: refuse only
      when the slot holds a live node.
- [x] (second pass) `Set.Execute` skipped a pre-removed value whenever
      elementMap held anything under its createdAt, a tombstone included, so
      the outcome depended on how far local GC had run. Rejecting a removed
      Set value at the push boundary was considered and not taken: the JS
      Remove reverse copies the key's current value, which can be a
      tombstone. Fix: protect only a live occupant, which GC never purges.

## Review


- Red before each fix, Green after: `TestArraySetRedoAfterPeerRemovalKeepsTheTombstoneOut`,
  `TestSetElementRejectsImpossibleTickets`, `TestRefusedMemberInSnapshotStaysLoadable`,
  `TestLosingUndoRestoreConverges` (green on `main`, red with the tombstone
  refusal), and the Set subtest judging a pre-removed value before and after
  collection.
- Not this change: whichever of two concurrent writers evicts a value first
  stamps its removedAt, so a displaced value's tombstone can differ between
  replicas by delivery order. `main` has the same behavior.
- Follow-up for yorkie-js-sdk: its ArraySet reverse still copies a removed
  value, which the Go server now accepts as an array element.
- `make verify` and `make test` green.

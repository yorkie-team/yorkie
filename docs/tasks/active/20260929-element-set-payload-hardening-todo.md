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

## Review


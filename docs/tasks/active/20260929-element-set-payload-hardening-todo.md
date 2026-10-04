**Created**: 2026-09-29

# Harden Set against payloads no replica can produce

> **Scope since 2026-10-04:** this PR is the push-boundary validator only
> ("PR B"). The CRDT-side refusals listed under "Carried over" and "Code
> review" moved to #2100 or were dropped; see "Round 2 (panel, 8978a450)" at
> the end and `docs/design/pushed-payload-validation.md`.

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

## Round 2 (panel, 8978a450)

The bench job hung for 6h on this head. `BenchmarkRPC/attach large document`
has two clients `SetNewText("k1")` before attach, so both texts share one
`createdAt` (InitialActorID, same lamport). The live-slot refusal in
`ElementRHT` refused the losing text, both clients' edits landed on the
surviving one, and the document grew past 16 MB. That refusal is keyed on a
collision a legitimate history produces.

- [x] Drop every replicated refusal from this PR: `ElementRHT`, `Object`,
      `Root.UnregisterRemovedElementPair`, the json layer and the
      `Set.Execute` guards go back to `main`. The CRDT fix is #2100; the
      ArraySet reverse change ships separately. Set, Add and ArraySet are
      now consistent: none refuses on apply.
- [x] Add and ArraySet values get the push-boundary rules (blocking:
      "Add.Execute and ArraySet.Execute have no createdAt-collision guard").
      Value not created after its operation, for all three; an Add value
      carries no removedAt. A collision with an element elsewhere in the
      document is not checked -- it needs the document, and pre-attach
      collisions and concurrent undo restores are legitimate on `main`.
- [x] Duplicate createdAt among one object's members (blocking). The
      validator walks the protobuf, not the decoded tree, and refuses with
      `ErrRefusedMember`. The untombstonable-loser rule is replayed on the
      protobuf too, so it no longer depends on `ElementRHT` reporting a
      refusal.
- [x] `FromPushedChangePack` has a test (blocking): `TestFromPushedChangePack`
      checks a legitimate pack decodes and a crafted op in any change rejects
      it.
- [x] Document-level redo test asserting the lenient decoder (blocking):
      removed with the ArraySet reverse change. Its history is now a subtest
      of `TestPushBoundaryAcceptsReplicaHistories`, which requires the exempt
      shape to be present and pushes it through `FromPushedChangePack`.
- [x] Detach/Remove dropping a refused pack (blocking): `FromLeavingChangePack`
      does not exist on this branch. Detach refuses a crafted pack like the
      other RPCs; `TestPushedPayloadValidation` (integration) pins Attach,
      PushPull and Detach refusing it with InvalidArgument, nothing reaching
      the document, and an honest change at the same client seq going
      through.
- [x] json `setInternal` returning a detached element (blocking): gone with
      the json-layer revert.
- [x] Stored-change replay running new Execute refusals (blocking): gone; no
      Execute path changes.
- [x] Log a refused push with client and document (`fromPushedChangePack` in
      `server/rpc/yorkie_server.go`).
- [x] Design doc: `docs/design/pushed-payload-validation.md`.

### Verification

- `TestPushBoundaryAcceptsReplicaHistories`: object and array undo/redo,
  ArraySet redo after a peer removal, the concurrent-undo history of
  `TestConcurrentUndoRestoresSameValue`, and two clients setting one key
  before attach. Every pack passes `FromPushedChangePack`.
- `TestValidatePushedValues`, `TestValidatePushedObjectMembers`,
  `TestSetElementRejectsImpossibleTickets`.
- `TestPushedPayloadValidation` (integration, MongoDB).
- `BenchmarkRPC` at default benchtime.

### Still open

- A client that already holds a rejected change has no repair path.
- Re-check the value rules when element `RestoreMode` ships, and against the
  mobile SDKs.
- Archive these task docs before merging.


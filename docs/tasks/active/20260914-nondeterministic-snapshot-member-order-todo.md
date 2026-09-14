# The same document encodes to a different snapshot on every attach

**Created**: 2026-09-14

A live document served a key as present on one attach and absent on the next,
from storage that never changed. The randomness is ours: `ElementRHT.Nodes`
ranges a Go map, and `api/converter` emits an object's members from it, so
every attach re-encodes the same object in a different member order. The
member order of an encoded object is not an implementation detail — a peer
rebuilds the object by replaying `SetWithExecutedAt` over it.

Go's own decoder is order-independent, so this was invisible from Go. The JS
SDK's `ElementRHT.set` is not (filed separately in
`20260816-remote-redo-replica-divergence-todo.md` § the `SetWithExecutedAt`
note), so a browser client resolved the same key differently per page load.

## What was measured

A wafflebase slides document (`slides-deb5465a…`), read with
`@yorkie-js/sdk@0.7.21` against the internal service, attached 12 times.
Same document, no writes between attaches, `doc.toJSON()` each time:

| attach | `50f8cc21.frame` | `e4f7414b.frame` | bytes |
| --- | --- | --- | --- |
| 1 | absent | present | 17311 |
| 2 | present | absent | 17325 |
| 3 | absent | absent | 17227 |
| 4 | present | present | 17409 |
| … | | | |

Three of twelve were clean. Pinning to one server pod did not stabilize it,
which ruled out a per-pod cache: two attaches to the same pod disagreed.
MongoDB reads were stable across repeats, which ruled out storage.

The CRDT internals name the shape. Under `50f8cc21.frame`, 27 nodes:

    created=354:f7:1  moved=368:f7:1  removed=-         positioned=368:f7:1   <- live
    created=353:f7:1  moved=367:f7:1  removed=368:f7:1  positioned=367:f7:1   <- tombstone
    created=326:d9:1  moved=364:f7:1  removed=367:f7:1  positioned=364:f7:1
    …

`createdAt(354) < 367 < positionedAt(368)`. A live member whose `movedAt` is
newer than its `createdAt` is what an undo/redo restore leaves behind, and it
is the only shape where a decoder can tell the arrival order apart.

## Fix

Two changes, both about making the encoder's output a function of the
document alone:

1. `crdt.ElementRHT.Nodes` returns nodes in ascending `PositionedAt` order
   (tie-broken by `createdAt`) instead of Go map order. An object's members
   reach the wire as a **repeated** field, so their order is
   protocol-visible.

   Ascending `PositionedAt` is chosen over ascending `createdAt` because it
   is *replay* order: every node then arrives with a ticket newer than the
   one occupying its key, so the LWW comparison always resolves forward.
   `SetWithExecutedAt` does not need that — it is order-independent — but a
   JS client that has not taken the `element_rht.ts` fix does. The guarantee
   assumes no two nodes under one key share a `PositionedAt`, which
   per-operation tickets make unreachable; the `createdAt` tie-break restores
   determinism, not the forward-replay property.

   It is not free. `ObjectToBytes` on a 1000-member object goes 495µs → 616µs
   (**+24%**), and 50 members 29.9µs → 34.6µs (+16%); the cost is
   `Ticket.Compare` → `ActorID.Compare`, and `slices.SortFunc` recovers only
   a couple of percent. Snapshot encoding runs per attach and per snapshot
   interval, so `BenchmarkSnapshotEncoding` is added to `test/bench` — nothing
   there exercised a wide object.

2. `api/converter` marshals through `proto.MarshalOptions{Deterministic:
   true}`. Protobuf **map** fields (text / tree node attributes) are
   serialized in randomized order independently of anything we control.
   Map fields are unordered by definition and every decoder reads them into
   a map, so this changes no meaning — it is what lets a test assert "the
   same document always encodes to the same bytes" at all.

`crdt.RHT.Nodes` is deliberately **not** sorted. Both of its encoder
consumers (`toTextNodes`, `toRHT`) copy it into a Go map keyed by
`node.Key()`, which becomes a protobuf map field — the slice order is erased
before it reaches the wire, so item 2 already covers text and tree
attributes. Sorting there would be pure cost on the text-editing path
(`TextValue.DataSize` → `attrs.Nodes()`), measured at +7% wall and +8% allocs
on a 200-edit style benchmark. Verified both ways: with the sort removed and
`Deterministic` kept, `TestSnapshot*` is green; with the sort kept and
`Deterministic` dropped, it fails.

## Tasks

- [x] `TestSnapshotDecodeIsOrderIndependent` — encode, enumerate **every**
      permutation of the wire members, decode each, assert the same object.
      Covers undo and undo+redo. Also asserts each permutation re-encodes to
      the canonical bytes, so no arrival order may leave the tombstones
      carrying different timestamps.

      Run at `f823fcf1`, its **object-equality** half passes on all 8
      permutations — that half is the baseline the JS SDK is being brought to,
      and it is the evidence that Go's decoder was never implicated. Its
      **byte** half cannot pass there, because it inherits the very encoding
      nondeterminism under test: 8 failures, all at the `bytes.Equal`
      assertion, none at `obj.Marshal()`.
- [x] `TestSnapshotEncodingIsDeterministic` — encode the same object 100
      times, assert byte equality. Four cases: an object whose key was
      restored by undo, a plain multi-member object, a nested object (the
      recursion in `toJSONObject`), and a text node with attributes (the
      protobuf-map case). All failed before the fix.
- [x] `TestElementRHTNodeOrder` — ascending `PositionedAt`, the same order on
      every call (the property the change exists for), and a `PositionedAt`
      tie resolved deterministically. All three fail at `f823fcf1`.
- [x] Sort `ElementRHT.Nodes` by `PositionedAt`. Leave `RHT.Nodes` alone.
- [x] Marshal deterministically in `api/converter`.
- [x] `go test ./...` green, `make lint` 0 issues.
- [x] `go test -tags integration ./...` against a local MongoDB — 23 packages
      ok, no failures (`test/integration` 76.5s).
- [x] `BenchmarkSnapshotEncoding` in `test/bench`, so the comparator's cost
      stays visible.

## Verified end to end

Reproduced against real servers, not just in-process. A writer client does
`set; set; undo` and syncs; then N independent clients attach and read the
key back. The project is created with `--snapshot-threshold 1
--snapshot-interval 1`, because with a handful of changes the server sends
the change history rather than a snapshot, and applying operations in causal
order never exercises the decode path.

| server | `@yorkie-js/sdk` | reads that saw the key |
| --- | --- | --- |
| `yorkieteam/yorkie:latest` | 0.7.21 (unfixed) | 6/12, 11/12, 0/12 |
| this branch | 0.7.21 (unfixed) | 12/12, 12/12, 12/12 |
| `yorkieteam/yorkie:latest` | this branch's JS fix | 12/12 x5 |

The first row is the reported symptom: three fresh documents, identical
writes, and the key present on some reads and absent on others — the reader
sees `{}` instead of `{"frame":"v1"}`. The second row is what this change
buys on its own, and is why the emitted order is `PositionedAt` ascending
rather than `createdAt` ascending. The third is the JS half standing alone.

## This is a mitigation, not the fix

The defect is `yorkie-js-sdk`'s `ElementRHT.set`. What this change removes is
the randomness that turned that defect into a per-attach coin flip; a client
that has not taken the JS fix still resolves the key wrongly whenever it
receives members in a disagreeing order from anywhere else. It also only
takes effect for a given document once every pod serving it has rolled, so
the symptom persists on old pods during a rolling upgrade.

No format change, no migration, and no `CHANGELOG.md` edit (this repo touches
that file only in release commits).

## Not in this change

- The JS half. `yorkie-js-sdk`'s `ElementRHT.set` gates eviction on the
  occupant's raw `createdAt` while gating the winner on its `positionedAt`;
  when an older write arrives after a newer one it tombstones the winner and
  leaves it linked under the key, so `get` reads the key as absent. That is
  the defect this ordering change hides rather than fixes, and it is fixed in
  the matching `yorkie-js-sdk` branch.
- `server/backend/database/change_info.go:91,165` still marshal stored
  operation bytes with a bare `proto.Marshal`, so a `TreeStyle`'s attribute
  map stays nondeterministic there. Harmless today — operations are encoded
  once at write and read back verbatim — but a trap for any future work that
  compares or hashes encoded change bytes.
- The redo/GC divergence in
  `20260816-remote-redo-replica-divergence-todo.md`. Same family — both need
  a key to hold a tombstone beside an undo-restored live member — but a
  different reach.

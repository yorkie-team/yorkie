# DocSize: a snapshot rebuild over-credits one ticket per tombstone

**Created**: 2026-08-17

Found while measuring the container-removal fix
(`20260816-root-docsize-nested-container-gc-todo.md`) and confirmed to be
**pre-existing on `main` and unchanged by that fix** — the figures below are
byte-identical before and after it. Filed separately because the repair
changes the reported size of every snapshot-rebuilt document, which is a
different blast radius from the accounting fix that surfaced it.

## Problem

`Root.RegisterRemovedElementPair` (`pkg/document/crdt/root.go`) refunds one
`time.TicketSize` to `docSize.Live.Meta` when it moves an element's size into
GC. The refund exists because, on the incremental path, `RegisterElement`
booked the element into `Live` *before* it had a `removedAt`, while the
removal subtracts its size *after* — one ticket larger. Without the refund
`Live` would lose a ticket it never held.

`NewRoot` (`root.go:64-108`) does not work that way. It calls
`RegisterElement(root)` on a tree whose tombstones **already carry
`removedAt`**, so `Live` is seeded with the post-removal size, ticket
included. The per-tombstone pass that follows then calls
`RegisterRemovedElementPair` for each removed element, which moves that same
size out again — correctly — and refunds a ticket that `Live` did hold. The
refund is unmatched, once per tombstone.

## Measurement

Four sibling members removed one at a time, taking
`InternalDocument().DeepCopy()` after each removal and comparing the rebuilt
`Live.Meta` against the incrementally maintained one:

| uncollected tombstones | rebuilt − incremental |
|---|---|
| 1 | +24 |
| 2 | +48 |
| 3 | +72 |
| 4 | +96 |

Linear in tombstone count, in both `main` and the container-removal fix.

## Why it matters

The rebuilt figure is the **enforced** one. `Document.Update` gates
`MaxSizeLimit` on `d.cloneRoot.DocSize()` (`pkg/document/document.go:257-258`)
and `cloneRoot` is produced by `ensureClone` → `Root.DeepCopy` → `NewRoot`
(`document.go:861-868`). Meanwhile `Document.DocSize()` (`document.go:836`)
reports `d.doc.root`'s incrementally maintained figure. So the number a caller
reads and the number the limit is checked against diverge, by a ticket per
uncollected tombstone.

The direction is conservative — the enforced size is too *large*, so the limit
trips early rather than late, and nothing is under-billed. But a document with
many uncollected tombstones (GC is version-vector gated, so tombstones can
persist for a while) can be refused an update while reporting a size below the
limit, with no way for the caller to explain the gap.

`server/packs`'s snapshot path builds its documents the same way
(`BuildInternalDocForServerSeq` → `applySnapshot` → `NewRoot`), so a
server-side size check sees the rebuilt figure too.

## Tasks

- [ ] Decide the fix shape. The cleanest framing is to make registration
      decide the bucket once, rather than add-to-Live-then-move: have
      `RegisterElement` route an element that already carries `removedAt`
      straight into GC (and into `sizeInGC`) instead of into `Live`, so
      `NewRoot` never books the ticket into `Live` and the refund never
      applies there. That keeps the "counted in exactly one of Live or GC"
      invariant a registration-time decision.
- [ ] Add a regression test asserting `DeepCopy()` and a
      `ObjectToBytes`/`BytesToObject` round trip both reproduce the source
      document's `DocSize` exactly, with tombstones present. Today's
      `TestDocumentSize/deep copy test` only covers documents with no
      removals, which is why this went unnoticed.
- [ ] Check whether `RegisterGCPair`'s `gcOnlySize` path
      (`pkg/document/crdt/root.go`, and the JS twin in `crdt/root.ts`) has the
      same build-vs-edit split. PR #1294 ("Separate correction logic in
      RegisterGCPair for editing and build") fixed exactly this shape for the
      node-level pairs; the element-level pairs never got the same treatment.
- [ ] Mirror whatever lands in `yorkie-js-sdk`: `registerRemovedElement`
      (`packages/sdk/src/document/crdt/root.ts`) has the same unconditional
      `docSize.live.meta += TimeTicketSize`, and `CRDTRoot`'s constructor the
      same seed-then-move pass.

## See Also

- `docs/tasks/archive/2026/08/20260816-root-docsize-nested-container-gc-todo.md` —
  the fix whose measurement surfaced this
- `docs/design/snapshot-overflow.md` — the other place snapshot size and
  live size are reasoned about together

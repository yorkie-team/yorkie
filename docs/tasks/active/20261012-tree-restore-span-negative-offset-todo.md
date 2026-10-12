# Tree restore: a span whose own ID.Offset is negative is acted on

**Created**: 2026-10-12

Follow-up filed from the review of #2175 (issue #2180). The finding is about
decode/apply code that #2175 did not touch, so it was deferred out of that PR
and into this one. The fix lands here: the checklist below is the work of this
change, not of a change still to come.

## Problem

A `TreeRestoreSpan` carries the identity (`createdAt` + `offset`) that
`crdt.Tree.Restore` materializes into authoritative server state: a recreated
node is registered in `NodeMapByID` under exactly that id.

Nothing rejects or skips a span whose own `ID.Offset` is negative:

- `fromTreeNodeID` (`api/converter/from_pb.go:1179`) passes the offset through
  unchanged, and says so deliberately — it is shared with the stored-change and
  snapshot read paths, so rejecting there would make a document that already
  holds such a value permanently unrebuildable, and flooring an *identity* (as
  opposed to an anchor) would merge two nodes.
- `fromTreeRestoreAnchorID` (`from_pb.go:1213`) coerces only the span's LEFT
  ANCHOR, which is resolved by position rather than keyed by identity.
- `fromTreeRestoreSpans` (`from_pb.go:1247`) validates `Length` and the
  presence of every `created_at`, but not the span id's offset.
- `operations.validateTreeRestoreIdentities`
  (`pkg/document/operations/tree_edit.go:1101`) checks the ticket's causality
  only — never the offset.
- `crdt.Tree.Restore` / `recreateFromSpan` / `recreateParent` do no bounds
  check either.

No honest producer can emit one: a span's id is copied off a real node, and a
node's offset is a non-negative index into its insertion. So a negative offset
only ever arrives from crafted input.

Consequence. `recreateParent`'s claim check (`tree.go:1391`) asks whether a
surviving node already holds the id. With a negative offset the floor probe
lands on some *earlier* insertion's node, whose offset is never `-1` and whose
text interval test is vacuously satisfied, so the check does not speak to the
identity being recreated at all. The span is then recreated and registered
under an id no node of that insertion ever occupied, below the whole
insertion's offset range — the straddling registration the companion finding on
`tree.go:1391` describes.

## Approach

Skip the span in `Restore` and `Retombstone`, the way a span whose kind
disagrees with itself is already skipped (`kindAgrees`, #2168).

Flooring the identity to 0 is not available: it would point the span at a
*different*, real identity — the reason `fromTreeRestoreAnchorID` floors only
the left *anchor*, which resolves by position rather than by identity.

The skip is a pure function of the span itself, so every replica carrying the
rule skips the same span, and a replica that still holds the named node is
unaffected (it has no negative-offset nodes to match).

The skip is the apply-time half and does not reject the change, so the server
still persists the crafted span and relays it to every peer. Closing that needs
the ingress half as well, at the wire boundary — see the checklist below. The
two cannot be the same edit: `fromTreeRestoreSpans` also runs over stored
operations via `FromStoredOperations`, so a bare rejection there would make a
document that already contains such a span unloadable forever. The codebase
already has the shape for this split — a wire rejection paired with a
stored-path repair in `NormalizeStoredOperations` (`api/converter/normalize.go`,
as `SplitLevel < 0` has) — which is why the "rejection is unavailable here"
reading of `fromTreeRestoreAnchorID`'s comment does not hold for an identity
offset.

## Checklist

- [x] `TreeRestoreSpan.offsetAddressable()` in `pkg/document/crdt/tree.go`
- [x] Guard both `Restore` and `Retombstone` with it
- [x] Unit tests: element span, text span, retombstone, and a check that the
      tree is untouched and `NodeMapByID` gains no entry
- [x] `make lint` + `go test ./pkg/document/...`
- [ ] **Ingress rejection at the wire boundary, in `api/converter`.** The skip
      above changes only what this replica does; the change is still accepted,
      persisted and fanned out, so the crafted span reaches every peer. Reject
      `pbSpan.Id.Offset < 0` in `fromTreeRestoreSpans` (`from_pb.go:1247`),
      beside the `Length < 0` rejection already there, and clamp or drop the
      span in `NormalizeStoredOperations` (`normalize.go:123`) so a span
      persisted before the check keeps its document loadable. Both files are
      outside this change's diff, so this lands as its own change.
- [ ] **JS mirror in `yorkie-js-sdk`, as the sibling `kindAgrees` rule has
      (yorkie-js-sdk#1471).** The skip changes what an applied operation does,
      so a JS replica without it recreates a node the Go replica drops and the
      two states diverge. In `CRDTTree.restore` and `CRDTTree.retombstone`,
      skip a span whose `id.offset` is negative, in the same `continue` that
      already skips a span whose `isText` disagrees with its `nodeType`.
      Until that lands, the two SDKs converge only on documents that never
      carry a negative-offset span — which no honest producer emits, so the
      divergence is reachable from crafted input alone.

## Out of scope

A *positive* offset that no node of the insertion ever occupied cannot be told
from a valid one without the insertion's total length, which neither the
decoder nor `Restore` has. The `recreateParent` claim check is what covers the
case where such an offset collides with a surviving node.

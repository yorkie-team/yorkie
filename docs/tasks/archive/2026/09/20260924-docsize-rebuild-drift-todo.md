# docSize drifts from a rebuild in three places

**Created**: 2026-09-24

`docSize.Live` and `docSize.GC` are running accumulators: an operation reports a
diff and it is added, never recomputed. The ground truth is a rebuild —
`InternalDocument().DeepCopy()` on the server — which recomputes both halves
from the content. `MaxSizeLimit` is enforced against the accumulator, so a term
the accumulator skips is quota a document gets for free.

Filed as yorkie-team/yorkie#2017. Three independent places, one invariant:
**a document's size is a function of its content, not of its edit history or of
the order the edits arrived in.**

## 1. `MoveAfter` never charges Live for the `movedAt` it stamps

`RGATreeList.MoveAfter` stamps `entry.elem.SetMovedAt(executedAt)`, and every
element's `MetaSize` counts that ticket. `Move.Execute` registers only the dead
position node (as `GCOnlySize`) and adds nothing to Live, so the first move of
an element hides one `TicketSize` (24 bytes) from the ledger. Later moves of the
same element overwrite the ticket and cost nothing more, so the drift is
**24 bytes per moved element**, up to 24 × n on a reorderable list.

Direction: the ledger **under**-reports, so the limit is too permissive. The
document silently exceeds its quota, and the first load through a path that
recomputes from content — a snapshot restore — makes the reported size jump and
previously-fine edits start failing with no content change to explain it.

**Fix.** `MoveAfter` already distinguishes the first stamp (`entry.posMovedAt ==
nil`) from an overwrite, and the LWW-discard branch stamps nothing at all. Have
it report the Live diff; `Move.Execute` and the two `json.Array` move helpers
accumulate it.

## 2. `RHTNode` size disagrees between server and SDK across the wire

Go charges `(len(key) + len(value)) * 2` on the **stored** string. The JS SDK
charges the *logical* value, stripping one layer of JSON encoding
(`logicalValue` in `crdt/rht.ts`), because `stringifyObjectValues` keeps quotes
only on a string that itself parses as a JSON document. The two agree on the
common case and diverge on that subset: `{b:'1'}` is stored as `"\"1\""` and
charged 8 by Go, 4 by the SDK.

The issue offers two fixes — decode one JSON layer in Go, or drop `logicalValue`
from the SDK. Only the first is reachable from this repository; the second lives
in `yorkie-js-sdk`. Take the first: `RHTNode.DataSize` sizes the logical value,
short-circuiting on the leading quote so the common case costs one byte compare.

## 3. `RHT.Remove` carries the value it replaced into the tombstone

`RHT.Remove` builds its tombstone as `newRHTNode(k, node.val, …)` — copying
whatever it replaced — but writes `""` when the key was absent. So the
tombstone's bytes depend on what had landed at that key when the removal
arrived, and concurrent same-key `style` + `removeStyle` charge differently
depending on delivery order, for identical rendered output.

**Fix.** Write `""` into the tombstone, making its bytes a function of the key
alone. That is *not* a one-line change, because two accounting paths currently
rely on the tombstone being byte-identical to what it replaced:

- `Root.RegisterGCPair` subtracts `Child.DataSize()` from Live, so a smaller
  tombstone would strand the value's bytes in Live forever.
- `Root.collect` subtracts `Child.DataSize()` at purge, while the enclosing
  node's GC charge (taken when the node was removed) still included the
  then-live attribute, so a smaller tombstone would strand them in GC.

So `Remove` also reports the bytes the dropped value stops occupying, and
`Text.RemoveStyle` / `Tree.RemoveStyle` take them out of Live when the node
holding the attribute is live, and out of GC when it is already a tombstone.

## Out of scope

- `Document.getDocSize()` returning `DocSize` by reference: that is the JS SDK.
  Go's `Root.DocSize()` returns a value, so the same test cannot read `0`.
- The symmetric `move_operation.ts` and `crdt/rht.ts` changes: `yorkie-js-sdk`.

## Plan

- [x] 1. `MoveAfter` reports the first-stamp Live diff; three callers accumulate.
- [x] 2. `RHTNode.DataSize` sizes the logical value.
- [x] 3. `RHT.Remove` mints a value-less tombstone and reports the dropped bytes.
- [x] Unit tests that compare the accumulator against a rebuild, and compare all
      delivery orders of a concurrent style/removeStyle against each other.
- [x] `make lint`, `go test ./pkg/... ./api/...`.

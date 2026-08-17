# DocSize: port the container-removal accounting fix to the JS SDK

**Created**: 2026-08-17

The Go fix for `Root.RegisterRemovedElementPair`
(`20260816-root-docsize-nested-container-gc-todo.md`) leaves `yorkie-js-sdk`
with the same bug. Filed so the gap is tracked rather than living in a
paragraph of the Go task, matching how PR #1483 cross-linked its JS
counterpart (`yorkie-team/yorkie-js-sdk#1086`).

## What reproduces

`registerRemovedElement` (`packages/sdk/src/document/crdt/root.ts:257`) adds
only `element.getDataSize()` to `docSize.gc` and subtracts it from
`docSize.live`, with no descendant walk — while `deregisterElement`
(`root.ts:232-253`) subtracts the element **and every descendant**. Identical
asymmetry to Go's.

Running the same four scenarios through the JS SDK produced figures identical
to Go's pre-fix output:

| scenario | JS `gc` after collection | Go on `main` |
|---|---|---|
| object with one member | `{data:-2, meta:-48}` | `{Data:-2 Meta:-48}` |
| array with one element | `{data:-2, meta:-24}` | `{Data:-2 Meta:-24}` |
| nested object | `{data:-2, meta:-96}` | `{Data:-2 Meta:-96}` |
| text | `{data:0, meta:0}` (correct) | `{Data:0 Meta:0}` (correct) |

`set_operation.ts:98-104` also has the wrong-element
`root.deregisterElement(value)` — it passes the incoming deep copy rather than
the element registered under that `createdAt`.

## One place the SDKs genuinely differ

Go's `ElementRHT.DeleteByCreatedAt` returns nil when a remove loses the LWW
comparison, so `Remove.Execute` skips the registration entirely. JS's
`ElementRHT.delete` (`element_rht.ts:119-130`) ignores `node.remove()`'s
return value and `remove_operation.ts:100` registers unconditionally.

That is why, before the Go fix, two Go replicas removing the same container
concurrently ended up disagreeing with *each other*, while two JS replicas
were uniformly wrong. Making the size move idempotent removes the dependence
on this difference, so the JS port does not need to also change `delete`'s
return-value handling — but whoever does the port should know the difference
is there.

## Tasks

- [ ] Port the invariant, not just the descendant walk: every registered
      element's size counted in exactly one of `live`/`gc`, with a map
      recording which and how much. JS's `gcElementSetByCreatedAt` is a `Set`
      today and needs to become a map of charged `DataSize`, for the same
      reason Go's does — `getDataSize()` grows by a ticket when `removedAt` is
      set, which can happen after the size has moved.
- [ ] Fix `set_operation.ts:98-104` to deregister
      `root.findByCreatedAt(value.getCreatedAt())`, not `value`.
- [ ] Port the six regression tests from Go's
      `pkg/document/size_test.go` (`TestDocumentSize`): removing a non-empty
      container (object/array/nested), a container holding an earlier
      tombstone, concurrent removal of the same container, removing a member
      inside an already removed container, restoring a container over a
      diverged tombstone, and undoing the removal of an array container.
      `packages/sdk/test/unit/document/document_size_test.ts` is the home, and
      `gc_split_leak_test.ts` already has the `crossSync`/two-replica helper
      the concurrent cases need.
- [ ] Cross-link the resulting `yorkie-js-sdk` PR back to the Go PR.

## See Also

- `docs/tasks/active/20260816-root-docsize-nested-container-gc-todo.md` —
  the Go fix this ports
- `docs/tasks/active/20260817-docsize-snapshot-rebuild-drift-todo.md` — the
  adjacent rebuild drift, which JS shares too

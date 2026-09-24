**Created**: 2026-09-24

# Tree unwrap vs. merge-delete divergence (#1956)

## Problem

Two concurrent content-less `Tree.Edit` calls on `<r><p>ab</p><p>cd</p></r>`
leave the replicas with different children:

- client A: `Edit(0, 1)` removes p1's opening token, unwrapping it and
  hoisting `ab` into the root.
- client B: `Edit(0, 5)` removes p1 whole, including `ab`, and p2's
  opening token.

After a full sync A holds `<r>abcd</r>` and B holds `<r>cd</r>`. The `ab`
A hoisted survives on A even though B's delete covered it.

## Cause

§6.2 (`propagateMergeDeletes`) tombstones the children a prior merge moved
out of a node this edit deletes. It skipped that propagation whenever the
source's `mergedInto` pointed at this edit's own merge destination, reading
that as "a concurrent replica ran the same merge" rather than a delete.

On A, the unwrap moved `ab` into the root, so `p1.mergedInto == r`. B's
delete also merges into the root (`dest == r`), so the guard fired and `ab`
was left live — on B it had already been tombstoned inside p1.

The guard is still needed: for two replicas that run the *same* unwrap, and
for an edit whose own position sits inside the merged-away source, the moved
children must stay. Those are exactly the cases where one of the edit's own
positions names the source; a source the range merely spans is a plain
delete.

## Plan

- [x] Reproduce at document level with the existing two-replica harness in
      `pkg/document` (`exchangeInOrder`, `treeXML`, `treeShape`).
- [x] Narrow the §6.2 same-destination skip to the source named by the
      edit's own declared from/to position (`Tree.ToTreeNodes`, which reads
      the position before §1.1 redirects it).
- [x] Check for regressions with an exhaustive two-replica delete matrix
      over `<r><p>ab</p><p>cd</p></r>` and
      `<r><p><s>ab</s></p><p><s>cd</s></p></r>`: every pair of ranges,
      comparing XML and the ID-level shape.
- [x] Unit test in `pkg/document/tree_unwrap_merge_delete_test.go`: the
      reported case plus the two shapes the guard still has to protect.
- [x] Integration test in `test/integration/tree_test.go` mirroring the
      issue's reproduction.
- [x] Update `docs/design/concurrent-merge-split.md` §6.2 and the fix
      cross-reference (Fix 26).

## Known limitation

The replicas converge on visible content, but the hoisted children end up in
different sibling order: `mergeNodes` appends moved children to the end of
the destination, so each replica orders them by arrival. On the reported
case A ends with `[ab(x), cd]` and B with `[cd, ab(x)]` under the root.
Fixing that means placing merged children at the source's position instead
of appending, which changes the result of every unwrap (and would need the
same change in the JS SDK). Out of scope here; the visible trees agree.

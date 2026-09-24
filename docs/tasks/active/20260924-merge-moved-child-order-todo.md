**Created**: 2026-09-24

# Merge-moved children sit in arrival order

## Problem

`mergeNodes` appends the children it moves to the END of the merge
destination (`pkg/document/crdt/tree.go`, `dest.Index.MoveChild`). Two
replicas whose concurrent edits merge into the SAME destination each apply
their own merge first and the other second, so the two blocks of moved
children land in opposite order:

```
<r><p>ab</p><p>cd</p></r>
d1: Edit(0, 1)  -- unwrap p1, hoisting ab into r
d2: Edit(0, 5)  -- delete p1 whole and p2's opening token, hoisting cd into r

XML, both:   <r>cd</r>
r children:  d1 = [p1(x), p2(x), ab(x), cd]
             d2 = [p1(x), p2(x), cd, ab(x)]
```

`ab` is a tombstone on both replicas after #1956's fix, so XML and the live
node order agree and the convergence checks that compare rendered content do
not see it. The ID-level state still differs, and that is real divergence:

- a concurrent insert anchored to `ab` (sent by a third replica that saw the
  unwrap while `ab` was still live) resolves after `ab` on both, which is
  before `cd` on d1 and after it on d2 — the live order then differs too;
- the server builds snapshots in its own arrival order, so a replica that
  loads a snapshot takes whichever order reached the server.

Same class as "Concurrent splits of one boundary sit in arrival order"
(`20260923-same-boundary-split-order-todo.md`, yorkie-js-sdk#1373): a
placement rule that is arrival-ordered rather than ticket-ordered.

## Cause

`mergeNodes` has no placement rule at all — it appends. Nothing orders two
concurrent merges into one destination, and nothing ties a moved child to
where its source sat among the destination's children.

## Plan

- [ ] Failing test through protobuf like the wire: the case above, plus a
      third replica inserting against the hoisted child, plus a snapshot
      round-trip in each arrival order.
- [ ] Decide the placement rule. Two candidates, both wire-visible:
      - place moved children at the merge source's position in the
        destination (the source stays there as a tombstone), which also
        fixes the "unwrapping a non-last paragraph destroys the next one"
        limitation in `20260924-tree-unwrap-merge-delete-todo.md`; or
      - keep appending but order concurrent same-destination merges by
        ticket, the way §7.8 orders same-boundary split products. Note the
        interaction with plain inserts that land between two merged blocks,
        and with chained merges, where a child's `MergedAt` is the FIRST
        merge's ticket, not the one that moved it here.
- [ ] `docs/design/concurrent-merge-split.md`: new §6.x + Fix N row.
- [ ] **Mirror in yorkie-js-sdk before or with the Go merge.** The JS SDK
      appends too; either rule changes what a replica computes for every
      unwrap, so a patched server against an unpatched client diverges
      outright. Do not close this task until the mirror lands.
- [ ] Replace the `assert.NotEqual` in
      `pkg/document/tree_unwrap_merge_delete_test.go` (reported-case
      subtest) with the `treeShape` equality the other subtests assert.

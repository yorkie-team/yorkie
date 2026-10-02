# Lessons — reject mid-surrogate-pair indexes

**Created**: 2026-10-02

## Notes

- `index.Tree.FindTreePos` hands back a text node plus a UTF-16 *relative*
  offset, which is exactly the number `SplitText` would slice at. Validating
  there means the check and the split read the same number, instead of the
  check re-deriving one from the absolute index.
- At a boundary between two adjacent text nodes, `FindTreePos` and
  `splay.Tree.FindForText` both resolve to the left node at `offset == len`.
  That is never mid-pair, which is the right answer: if an old operation
  already split a pair across two nodes, editing at the seam splits nothing,
  so there is nothing new to reject.
- `crdt.Tree.FindPos` and `RGATreeSplit.createRange` are *not* local-only —
  `operations/tree_edit.go` uses `FindPos` on the reverse/undo path. The
  validation had to go in the `json` layer rather than in those, or undo of a
  pre-existing mid-pair edit would start panicking.

- The `json` layer reports caller mistakes by panicking, and `Document.Update`
  only discarded the clone on the *error* return. A guard that panics out of
  the updater therefore left every mutation the same updater had already made
  in `d.cloneRoot`, where `Document.Root` serves it — the root never took them,
  so `Root` handed back a state that exists on no replica. `Update` now
  recovers, invalidates the clone and re-panics; the defer is registered after
  `d.mu`'s unlock so it still runs under the lock.
- Validation must be exercised on a *split* text node, not just a freshly
  created one: both wrappers resolve the index to a per-node offset, so a
  single-node fixture cannot tell a correct offset resolution from one that
  only ever looks at the first node.

## Review round (panel)

Blocking finding across all three lenses: the new panic escaped `Update`
without `invalidateClone`. Fixed in `pkg/document/document.go`; the
multi-node and dirty-clone gaps are covered by three new tests in
`pkg/document/mid_surrogate_index_test.go`.

## Self review

Not run: this branch was produced by the autonomous issue-to-PR agent, which
is granted no tool that can dispatch the reviewer subagent. The round is
skipped, not clean — CI, `@claude review` and a human reviewer are the
review for this change.

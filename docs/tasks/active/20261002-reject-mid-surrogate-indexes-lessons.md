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

## Self review

Not run: this branch was produced by the autonomous issue-to-PR agent, which
is granted no tool that can dispatch the reviewer subagent. The round is
skipped, not clean — CI, `@claude review` and a human reviewer are the
review for this change.

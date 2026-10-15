**Created**: 2026-10-13

# Lessons: a peer's Tree undo leaves pending undo entries on stale indices

- Compare `treeShape` (IDs and tombstones) right before the failing step. Here
  both variants were identical, which ruled out the CRDT state and pointed at
  history reconciliation.
- A comment claiming a degenerate value is "harmless" needs a test. The
  `(0, 0, 0)` fallback was documented as a net-zero shift; it is a missed
  shift whenever the op changes visible content.
- A single-node repro cannot tell a per-node fix from an envelope fix. Add a
  case where the op touches both sides of a live node.
- Take expected strings from the other SDK when it already has the fix: it
  turns a parity port into a mechanical check.

## Self review

- Round 1 (correctness/tests, `/code-review high`): no correctness bug.
  Blocking: `docs/design/undo-redo.md` still said reconciliation reads
  `normalizePos()`/`getContentSize()` -- updated. Fixed: measure only on
  `OpSourceRemote` (a local undo/redo measured twice, clone and root, for
  nothing); dropped two matrix probes that pinned an unrelated, JS-shared
  defect as expected output. Logged a self-review round here.
  Not taken: `visibleRangeOf` does not log a failed measurement as JS does
  (`pkg/document/crdt` has no logger); measuring when the receiver's history
  is empty (JS does the same; skipping needs history plumbed into Execute);
  `ToIndex` scanning siblings twice (micro-optimization); a result struct
  for `Restore` (refactor outside this fix); the forward `Tree.Edit` path
  (follow-up in the todo, reproduces in JS).
- Round 2 (design fit, security/docs): no blocking finding, so the loop
  stops here. Parity with JS #1421 and the `OpSourceRemote` gate checked
  against every `ExecutedRanges` reader; `visibleRangeOf` cannot panic or
  loop on crafted spans. Fixed comment/doc drift: a stale JS OpInfo
  reference in `TreeEdit.Execute`, the unmeasured case on `executedRanges`,
  `OpSourceReplay`'s skip list, and the design doc's API bullets. Left as a
  known limitation: a remote op is measured on both the clone and the root.

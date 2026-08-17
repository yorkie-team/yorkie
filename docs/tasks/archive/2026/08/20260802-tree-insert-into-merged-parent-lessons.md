# Lessons: converge concurrent insert into a merged parent

**Created**: 2026-08-02

- Fix at the layer where the invariant is actually broken. The visible
  symptom pointed at position resolution (`FindTreeNodesWithSplitText`),
  but the real defect was upstream: the merge dropped the RGA anchor.
  Patching the resolver produced a plausible-but-wrong fix that a code
  review broke with a two-concurrent-insert case. Moving the fix into
  the merge (preserve tombstone anchors) let the existing, well-tested
  RGA path do the ordering — smaller diff, fewer edge cases.
- The concurrency test harness (`RunTestTreeConcurrency`) `t.Skip`s on
  divergence instead of failing. A divergence bug can hide as a skipped
  subtest. Baseline pass/skip/fail counts before a CRDT change and diff
  them after — a new SKIP is a silent regression.
- A single-interleaving convergence test is not enough for CRDT work.
  The first attempt passed the 2-client test but diverged with 3 clients
  inserting at the same anchor. Add multi-insert and reversed-order cases.
- Index length accounting: `remove()` subtracts a node's length from its
  *ancestors* and keeps the node's own `VisibleLength`. So relocating a
  tombstone with the alive-node `DetachChild`/`Append` pair double-counts
  its visible length (over-adds on the target, over-subtracts on the
  source). The first fix only patched the target; a review caught the
  source side. The right fix is a dedicated `index.Node.MoveChild` that is
  visible-neutral for removed nodes on both parents (moves only
  `TotalLength`). Lesson: when a primitive is documented "for alive nodes",
  don't reuse it on tombstones with a patch — add the correct primitive.
- Two independent review rounds each killed a wrong-but-plausible fix that
  passed the current tests. For CRDT convergence work, adversarial review
  plus multi-client/multi-shape tests are worth more than a green run.

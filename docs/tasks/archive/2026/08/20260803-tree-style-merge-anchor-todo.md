# Tree: keep style ranges from crossing a concurrent merge anchor

**Created**: 2026-08-03

Concurrent `Tree.Style`/`Tree.RemoveStyle` whose range ends inside an
element removed by a concurrent merge styled a node concurrently
inserted at the merged anchor on the applying replica — replicas
diverged on attributes. Found by the property-based Tree test for
yorkie-team/yorkie-js-sdk#1298 right after Fix 19/20 landed.

## Problem

Initial `<r><p>ab</p><p>cd</p></r>`. Concurrently:
- A: `edit(8, 8, <p/>)` — insert empty `<p>` after p2, then
  `style(0, 5, bold=x)` — range ends inside p2; the inserted `<p>` is
  outside the styled range on A's view
- B: `edit(0, 5)` — remove `<p>ab</p>` + p2 open tag (merge)

Replicas diverged:
- A: surviving inserted `<p>` unstyled
- B: the same `<p>` got `bold="x"` — the §1.1 insertion-boundary
  redirect resolved the style range end past it

## Plan

- [x] Reproduce with failing complex tests (RED): style, removeStyle
- [x] Add `BoundaryRange` mode to `FindTreeNodesWithSplitText`; use it
      from `Style`/`RemoveStyle` (position right after the merge-source
      tombstone)
- [x] Keep `BoundaryInsert` (§1.1) for edits
- [x] Positive case: range genuinely covering the merged content still
      styles it
- [x] Chained-merge case (p3 into p2, then p2 into p1) asserting the
      exact converged value
- [x] Unit test pinning the boundary indexes for both modes
      (`FindTreeNodesWithSplitText` insert vs range)
- [x] Design doc §9.3 + Fix 21 row
- [x] `golangci-lint run ./...` 0 issues; unit/complex/integration tree
      lanes green
- [x] Mirror in yorkie-js-sdk (same fix, `findNodesAndSplitText`
      `boundary` param)

## Out of scope

A range end anchored after a moved child (non-left-most position in
the merged parent) does not go through the §1.1 redirect and still
diverges — needs a per-node guard during traversal; tracked as
follow-up.

---
title: Lessons — TreeStyle divergence on concurrent split
created: 2026-04-10
status: todo
relates-to: 20260410-tree-style-split-divergence-todo.md
---

# Lessons — TreeStyle divergence on concurrent split

## Key Insight: Index-based vs Node-based Traversal

The fundamental difference between `Edit` and `Style` is how they traverse
the affected range:

| | Edit | Style |
|---|---|---|
| Traversal | `collectBetween` — walks nodes directly | `traverseInPosRange` — converts to indices, calls `TokensBetween` |
| Boundary handling | Per-node `canDelete` with VV check | Per-token callback with `canStyle` |
| Split sibling impact | Unknown nodes skipped by `canDelete` | Range itself crosses boundaries |

When `FindTreeNodesWithSplitText` resolves a position to a different parent
(because text moved to a split sibling), `collectBetween` handles it
gracefully — it walks nodes regardless of parent. But `traverseInPosRange`
converts positions to tree indices, and the index span now crosses an
element boundary that the editor never intended.

## Lesson 1: Fix 7 applies at the wrong abstraction level for Style

Fix 7 (`advancePastUnknownSplitSiblings`) operates on `leftNode`, advancing
past element-type split siblings. But the Style issue is about `parentNode`
resolution — when the text node moves to P', the parent changes from P to P'.
The `leftNode` (a text node) cannot be advanced because text nodes break out
of the InsNextID loop immediately.

**Takeaway**: Position forwarding fixes must consider which component of the
position (parent vs left sibling) is affected by the concurrent operation.

## Lesson 2: `canStyle` prevents styling unknown nodes but not range expansion

`canStyle` correctly rejects P' (the split sibling, unknown to editor). But
the range expansion from P to P' brings P's End token into the traversal,
and P IS known — so it gets styled via the End token.

**Takeaway**: Per-node guards alone are insufficient when traversal range
expands across split boundaries. A token-aware callback guard (End-token
suppression with split-sibling VV check) is required in Style paths.

## Lesson 3: CRDT position resolution assumptions

`FindTreeNodesWithSplitText` (line 1560) redirects `realParentNode` to
the left node's actual parent. This is correct for `Edit` (which uses
`collectBetween`), but incorrect for `Style` (which relies on parent-left
pairs for index computation).

**Takeaway**: When the same position resolution function serves multiple
callers with different traversal strategies, the resolution may need
caller-specific post-processing.

## Lesson 4: Property-based tests revealed what hand-crafted tests missed

The 6 divergences were invisible in the hand-crafted integration tests
because no test combined `splitLevel=1` with `Style` on overlapping ranges.
The property-based test suite (`TestTreeConcurrencySplitEdit`) systematically
enumerated all operation × range combinations and found the gap.

**Takeaway**: After fixing, add both a hand-crafted integration test (for
clear documentation) and verify the property-based suite flips from SKIP
to PASS (for exhaustive coverage).

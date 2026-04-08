**Created**: 2026-04-06

# Concurrent Merge/Split Convergence Fixes

**Spec:** `docs/design/concurrent-merge-split.md`

## Completed (7/10 tests enabled)

- [x] Add `DetachChild` method to index tree (`pkg/index/tree.go`)
- [x] Fix 1: Split sibling cascade delete via InsNextID chain (`collectBetween`)
- [x] Fix 2: Moved children guard for merge-boundary nodes (`collectBetween`)
- [x] Fix 3: Merge-tombstone insert redirect (`FindTreeNodesWithSplitText`)
- [x] Fix 4: mergedInto forwarding pointer + mergedChildIDs (`TreeNode`, Edit Step 04)
- [x] Fix 5: Delete propagation to children moved by prior merges (Edit Step 04-1)
- [x] Fix 6: Inverted range no-op in traverseInPosRange
- [x] Enable 7 previously-skipped tests

### Tests now passing

| # | Test | Fix |
|---|------|-----|
| 1 | overlapping-merge-and-merge | Fix 4 (mergedInto) |
| 2 | overlapping-merge-and-delete-element-node | Fix 5 (mergedChildIDs propagation) |
| 3 | overlapping-merge-and-delete-text-nodes | Fix 1 (cascade) + Fix 2 (guard) |
| 4 | contained-split-and-delete-the-whole | Fix 1 (cascade) |
| 5 | contained-merge-and-merge-at-the-same-level | Fix 4 + Fix 6 (inverted range) |
| 6 | contained-merge-and-insert | Fix 3 (redirect) |
| 7 | contained-merge-and-delete-contents-in-merged-node | Fix 3 (redirect) |

## Remaining (3/10 tests still skipped)

- [ ] `contained-split-and-split-at-different-levels` — multi-level split position resolution
- [ ] `side-by-side-split-and-insert` — split sibling ordering with concurrent insert
- [ ] `side-by-side-split-and-delete` — split text node visibility mismatch

### Root cause (shared)

All three share a common pattern: `SplitElement` creates a new element node
with a fresh `CreatedAt` (unknown to remote), but its text children inherit
the original `CreatedAt` (known to remote). When a concurrent operation's
traversal range passes through the split sibling, the text children are
visible (`canDelete=true`) while the parent element is not
(`creationKnown=false`).

Attempted fix (parent creation guard in `collectBetween`) failed because
the same conditions appear in both "text should be deleted" (contained delete)
and "text should be protected" (side-by-side delete). A solution likely
requires changes to `FindTreeNodesWithSplitText` to prevent the traversal
from passing through unknown split siblings.

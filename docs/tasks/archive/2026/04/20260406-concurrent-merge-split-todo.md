**Created**: 2026-04-06
**Completed**: 2026-04-10

# Concurrent Merge/Split Convergence Fixes

**Spec:** `docs/design/concurrent-merge-split.md`

All 10 originally-skipped concurrent merge/split integration tests now
pass. Design doc reports 63/63 convergence cases covered.

## Initial fixes (PR #1722)

- [x] Add `DetachChild` method to index tree (`pkg/index/tree.go`)
- [x] Fix 1: Split sibling cascade delete via InsNextID chain (`collectBetween`)
- [x] Fix 2: Moved children guard for merge-boundary nodes (`collectBetween`)
- [x] Fix 3: Merge-tombstone insert redirect (`FindTreeNodesWithSplitText`)
- [x] Fix 4: mergedInto forwarding pointer (`TreeNode`, Edit Step 04)
- [x] Fix 5: Delete propagation to children moved by prior merges (Edit Step 04-1)
- [x] Fix 6: Inverted range no-op in traverseInPosRange
- [x] Enable 7 previously-skipped tests

## Follow-up fixes

- [x] Fix 7: Split sibling forwarding — position-level advance past
      unknown split siblings in `FindTreeNodesWithSplitText` (#1723)
- [x] Fix 8: SplitElement skips merge-moved children via `MergedFrom`
      + `MergedAt` (#1724)
- [x] Fix 9: Skip merge for concurrent elements in `collectBetween`
      when the element's `CreatedAt` is unknown to the editor (#1725)
- [x] Fix 10: JS splitElement preserves tombstoned children (#1727)

## Tests enabled

| # | Test | Fix |
|---|------|-----|
| 1 | overlapping-merge-and-merge | Fix 4 (mergedInto) |
| 2 | overlapping-merge-and-delete-element-node | Fix 5 (mergedChildIDs propagation) |
| 3 | overlapping-merge-and-delete-text-nodes | Fix 1 (cascade) + Fix 2 (guard) |
| 4 | contained-split-and-delete-the-whole | Fix 1 (cascade) |
| 5 | contained-merge-and-merge-at-the-same-level | Fix 4 + Fix 6 (inverted range) |
| 6 | contained-merge-and-insert | Fix 3 (redirect) |
| 7 | contained-merge-and-delete-contents-in-merged-node | Fix 3 (redirect) |
| 8 | contained-split-and-split-at-different-levels | Fix 8 (MergedFrom) |
| 9 | side-by-side-split-and-insert | Fix 7 (split sibling forwarding) |
| 10 | side-by-side-split-and-delete | Fix 7 (split sibling forwarding) |

## Related follow-up

Snapshot roundtrip correctness for the merge runtime state was
addressed separately in `20260410-merged-fields-snapshot-todo.md`
(PR #1729) after this task completed.

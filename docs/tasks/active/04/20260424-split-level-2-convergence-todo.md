**Created**: 2026-04-24

# splitLevel>=2 Convergence

PR: https://github.com/yorkie-team/yorkie/pull/1776
Branch: `fix-split-level-2-convergence`
Design: `docs/design/concurrent-merge-split.md`

## Goal

Fix convergence divergences in concurrent `splitLevel >= 2` tree
operations. Ensure clone/root consistency (root tree structure matches
clone after change replay).

## Done

- [x] Fix 13: Propagate Fix 7 into recursive split loop with relaxed
  parent check
- [x] Fix 14: Include removed children in split loop offset
  (`FindOffset` with `includeRemoved: true`)
- [x] Fix 15: skipActorID — prevent advancing past own split products
  in recursive split loop
- [x] Fix 16: Move concurrent inserts at split boundary to the left
  in SplitElement
- [x] Fix 17: Move empty split sibling to existing InsNext sibling's
  parent in Split — resolves all 22 SplitSplit divergences
- [x] Add clone/root consistency check to `syncClientsThenAssertEqual`
  and `syncClientsThenCheckEqual` (integration + complex)
- [x] Add divergent-state XML logging to tree concurrency tests
- [x] Verify 0 clone/root mismatches across all test suites

## Remaining

- [ ] Fix concurrent split + insert parent placement
  (5 divergences in SplitEdit, see design doc "Remaining Issue")

## Test Results

### Integration (`test/integration/tree_test.go`)

| Category | Total | Pass |
|----------|------:|-----:|
| All tree tests | 100+ | 100+ |

### Complex (`test/complex/tree_concurrency_test.go`)

| Suite | Pass | Skip |
|-------|-----:|-----:|
| EditEdit | 901 | 0 |
| StyleStyle | 145 | 0 |
| EditStyle | 85 | 0 |
| SplitSplit | 321 | 0 |
| SplitEdit | 140 | 5 |
| **Total** | **1592** | **5** |

All 5 skipped cases involve concurrent `splitLevel >= 2` split with
insert/replace/delete where a non-split child lands in different
parent partitions.

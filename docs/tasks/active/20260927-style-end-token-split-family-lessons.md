# Lessons: a level-2 split before a style range

**Created**: 2026-09-27

## The complex suite cannot fail on divergence

`RunTestTreeConcurrency` reports a pair whose replicas disagree with
`t.Skip`, not `t.Fail`. A reached-set change that breaks convergence there
turns a pass into a skip and the lane stays green, on top of the lane only
running behind a `test/complex/**` path filter. The JS integration suite
asserts, which is why yorkie-js-sdk#1404 caught this and Go did not. Any
convergence claim for tree styles has to come from the unit-lane scans, so a
shape class the scans do not generate is unguarded.

## Scans have to cover the tree's shape, not only its operations

#2038's scans are exhaustive over one flat base and level-1 splits. A split of
more than one level moves a half into a new parent, which changes what §2's
advance does at a boundary; no flat base produces that. The nested scan
(42 splits x 276 ranges) found the #1404 pair among 363 divergences on main,
where the flat scans report zero.

## Compare three builds, pair by pair, not three totals

The per-pair dump over `ac88215d^`, main and the fix is what made the claims
checkable: 188 pairs converged before #2038 and diverge on main; the guard
closes 36 of those and 86 older ones, and turns no converging pair into a
diverging one. Totals alone (2810 -> 363 -> 241) would have hidden that 152
#2038 regressions remain, all a split of the range end's declared parent.
Those are a different rule (boundary elements, §9.5) and are left as a known
limitation so Go keeps exactly the JS guard.

## Review rounds

### Round 1 (correctness and tests)

An independent reviewer compared the guard with the JS one (`ToTreeNodes`
and `declaredParentOf` both give nil/undefined in the same cases), reran the
scans with the guard off (363 -> 241, no pair diverging only with the fix),
and looked for a family the guard wrongly drops. It found none: a range that
began before the known node reaches its Start token through the canStyle
branch instead, and one that began inside it or a descendant passes
`beginsInside`.

No blocking findings, so the review stops here. Non-blocking ones:

- Port spec rule 2 said "named that member as its parent", which is
  narrower than `beginsInside` (the member or any descendant). Reworded,
  and the overlong line rewrapped.
- The doc example used `{b: x}` while the test uses `bold: "aa"`. Aligned.
- The nested scans pin `rendered` as `<= 241`, but `Equal` on the styled
  counts catches any reached-set change. Kept.
- The JS `NOTE(js-only)` goes stale once this lands. That comment is in
  yorkie-js-sdk#1404, not here; flagged in the PR body.
- The todo checklist was unchecked. Filled in.

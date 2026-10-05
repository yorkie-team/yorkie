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

Round 1 had no blocking findings. Non-blocking ones:

- Port spec rule 2 said "named that member as its parent", which is
  narrower than `beginsInside` (the member or any descendant). Reworded,
  and the overlong line rewrapped.
- The doc example used `{b: x}` while the test uses `bold: "aa"`. Aligned.
- The nested scans pin `rendered` as `<= 241`, but `Equal` on the styled
  counts catches any reached-set change. Kept.
- The JS `NOTE(js-only)` goes stale once this lands. That comment is in
  yorkie-js-sdk#1404, not here; flagged in the PR body.
- The todo checklist was unchecked. Filled in.

### Round 2 (panel, correctness)

The review panel held that the begins-inside precondition is strictly
narrower than the branch it guards. The branch fires on an End token the
range ran past; the guard asks only whether the range STARTED inside the
known member. A range that began strictly before that member fails the
guard exactly as one that began after it does, and only the second never
covered the element. Round 1 answered this with "a range that began before
the known node reaches its Start token through the canStyle branch
instead" — true for the split-only shapes the scans cover, and not true
when a concurrent removal or the §9.4 from-side recovery moves the resolved
start past that node. That is the shape the panel named, and the scans
cannot produce it, so the round-1 search found nothing.

The guard is now `beginsAtOrInside`: begins-inside, OR the declared
range-start sits at or before the known member's Start token in document
order, both indices taken with removed nodes included so a concurrent
removal between them moves neither. A from-position that no longer resolves
leaves the answer at yes, matching the §9.6 guard's fail direction instead
of inverting it (the panel's second point). (Round 3 reverses this; see below.) Every scan count is unchanged —
the added disjunct fires on no pair in the split or merge families, which
is what "the traversal usually reaches such a node on its own Start token"
predicts — and the level-2 pair still converges.

The JS guard (yorkie-js-sdk#1404) is now the narrower half of this one.
Recorded in the design doc's known limitations: the order half has to land
there too, or the two implementations drop the closure on different shapes.

### Round 3 (maintainer, cross-SDK reproduction)

"Fires on no pair" was true, and it meant the order half had been added for
a shape nobody had produced. Counting each branch of the guard over the unit
scans and the complex suite gave zero decisive calls. One change that splits
and then deletes, raced against every style range, gave 13,014 decisive
calls, and on/off per pair gave 4,265 closed and 0 opened. A guard with no
counterexample is only as good as the generator that failed to find one;
widen the generator before trusting a rule, or before removing it.

The fail direction was measured, not argued. Answering yes on an
unresolvable from-position closed 368 pairs and opened 5, so it answers no,
the same as JS's `declaredFrom &&`.

The first JS port opened exactly those 5 pairs. Go's `FindOffset` fails when
the left sibling is no longer a child of its parent; JS's `findOffset`
returns -1 and `toTreePos` then resolves offset 0. The same predicate over
different error surfaces is a different predicate, so Port spec 2(c) now
names that case. Comparing the two SDKs pair by pair over one generator is
what found it; comparing totals would have shown 4,285 against 4,265 and
looked like JS doing better.

The same comparison surfaced 129 pairs that diverge in Go only, from a local
edit the two SDKs perform differently (a delete across a boundary a level-2
split just made). Recorded under Known limitations; it is not this rule.

# Tree: converge style ranges ending after a merge-moved child

**Created**: 2026-08-08

Fixes #1916 (yorkie-team/yorkie-js-sdk#1311). The follow-up variant
documented in #1909/§9.3: a style range end anchored after a moved
child resolves through the moved child directly, never reaching the
§9.3 boundary redirect, and styles a concurrently inserted node on
only one replica.

## Problem

Initial `<r><p>ab</p><p>cd</p></r>`. Concurrently:
- A: `edit(8, 8, <p/>)`, then `style(0, 6, bold=x)` — the range end is
  anchored after `c`, a non-left-most position inside p2
- B: `edit(0, 5)` — merge across the paragraphs

On B the inserted `<p>` sits between the p2 tombstone and the moved
`c`, inside the resolved range; on A it was outside. The `canStyle`
version-vector check cannot help: the interloper is A's own insert,
causally known but positionally excluded.

## Plan

- [x] Reproduce with failing complex test (RED)
- [x] Stamp inserts declared inside a merged-away parent with
      `MergedFrom = declared parent` (`intendedMergeParent`) so they
      stay distinguishable from never-inside nodes
- [x] Per-node filter in `Style`/`RemoveStyle`
      (`mergedAnchorInterloperGuard`): skip elements under the merge
      target, after the tombstone, without a matching `MergedFrom`
- [x] Guard-correctness tests: own insert inside the styled range
      stays styled; sibling before the tombstone stays styled
- [x] Judge interlopers by their highest ancestor under the merge
      target so descendants are skipped with them (found by probing
      a nested insert; diverged before this)
- [x] RemoveStyle-variant complex test (the one-sided empty RHT that
      Marshal hides); unit tests pinning the stamp in both repos
- [x] Triage remaining PBT counterexamples against main: the from-side
      style variant and a 3-client edit-only divergence both reproduce
      WITHOUT this change — pre-existing, tracked as follow-ups
- [x] Fail open on any merge-stamped node instead of exempting only
      `MergedFrom == declared parent`: children brought in by an
      earlier synced merge keep the original source (first-move stamp
      rule) and were wrongly skipped — diverged vs main before this
      (found by review; pinned by
      `TestTreeConcurrencyStyleCoversEarlierMergedChild`)
- [x] Copy `MergedFrom`/`MergedAt` in `SplitElement` (SplitText
      parity) so the filter judges split halves identically under
      either application order; unit test pins the copy
- [x] Pin the RemoveStyle guard against the Marshal blind spot: the
      complex test asserts no attribute entry materializes on the
      interloper (fails with the RemoveStyle guard disabled)
- [x] Fold the duplicated skip logic into `styleSkipPredicate`;
      precompute the after-tombstone set instead of a per-node
      linear scan
- [x] Design doc §9.4 + Fix 22; known-limitations list
- [x] `golangci-lint run ./...` 0 issues; unit/complex/integration
      tree lanes green
- [ ] Mirror in yorkie-js-sdk 

## Out of scope

- A range *start* anchored after a moved child shrinks the applier's
  traversal instead of growing it; a skip filter cannot compensate.
- An edit-only 3-client divergence (no style involved) reproduces on
  main independently of this change.
- Known-limitation corners listed in §9.4: earlier different-target
  merges fail open (pre-§9.4 behavior), relay-only merged parents
  never activate the guard, the `MergedAt` fallback reads LWW-mutable
  `removedAt`, and stamped inserts feed `propagateMergeDeletes` and
  the §1.1 redirect boundary scan.
All tracked as follow-ups; the first two are next-cycle issues from
the #1298 property-based test.

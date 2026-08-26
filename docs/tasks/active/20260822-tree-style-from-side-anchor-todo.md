# Tree: recover style ranges collapsed by a merge at the from anchor

**Created**: 2026-08-22

Fixes #1942 (yorkie-team/yorkie-js-sdk#1324). The from-side variant
documented as a §9.4 known limitation: a style range start anchored
after a merge-moved child collapses on the applying replica, which then
misses the nodes the styling client covered.

## Problem

Initial `<r><p>ab</p><p>cd</p></r>`. Concurrently:
- A: `Edit(8, 8, <p/>)`, then `RemoveStyle(6, 9, ["bold"])` — the range
  starts after `c` and ends inside A's own insert, so A's local
  application touches the insert
- B: `Edit(0, 5)` — merge across the paragraphs

On B the moved `cd` resolves behind the insert, so the resolved range
collapses (start past end) and the traversal styles nothing. Only the
writer carries the removal entry; `Marshal` hides the empty container,
but the asymmetric RHT state round-trips through snapshots. In the JS
SDK the same state is visible as a one-sided `attributes: {}`.

## Plan

- [x] Reproduce with failing complex tests (RED): style variant,
      RemoveStyle variant with the internal-state pin, ordered-range
      control
- [x] `reversedFromAnchorRecovery`: reuse the §9.4 shape detection for
      the *from* position; when the resolved range actually collapsed,
      re-anchor the traversal start after the last live sibling before
      the merge-source tombstone
- [x] Style only nodes the interloper predicate positively identifies;
      fail open (skip) for stamped or merge-moved nodes
- [x] Unit tests pinning the recovery and the ordered-range no-op
- [x] gocyclo relief: extract `stylePrevAttrs` and
      `styleClientLamportAt` (shared by Style/RemoveStyle)
- [x] Design doc: §9.4 from-side recovery (Fix 23), rewritten known
      limitations
- [x] Verify: unit, integration `TestTree*`, complex
      `TestTreeConcurrency*`, golangci-lint, gofmt
- [x] Triage remaining PBT counterexamples against main: a to-side
      variant (`RemoveStyle(6,8)` vs `Edit(1,5)`, styles the merge
      target one-sidedly) and the edit-only unwrap-vs-merge-delete
      divergence both reproduce on main before this change
- [x] Self review (multi-agent, both repos): confirmed the residual
      stamped-node shapes (moved element children, earlier-known-merge
      children) and the sibling-merge End-token shape all reproduce at
      base; recorded them in §9.4 and applied the cleanup findings
      (guard returns its derivations, recovery gate folded into the
      shared skip predicate)

## Out of scope

- The edit-only divergence (concurrent unwrap vs merge-delete) and the
  to-side moved-tombstone variant: pre-existing on main, documented in
  §9.4 known limitations, next report/fix cycle.

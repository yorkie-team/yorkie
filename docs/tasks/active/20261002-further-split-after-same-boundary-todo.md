# A further split after concurrent same-boundary splits lands on different sides

**Created**: 2026-10-02

Mirror of yorkie-team/yorkie-js-sdk#1435 (fixes yorkie-js-sdk#1433). The two
have to ship together: `orderSameBoundarySplit` is a replicated convergence
rule, and a snapshot built by a server without this change flips a patched
client (measured on #2030 / js#1375).

## Problem

```
<doc><p>ab</p></doc>, actor 1 < actor 2
d1: Edit(2, 2, nil, 1)   // split "a|b"
d2: Edit(2, 2, nil, 1)   // split "a|b", concurrently
d2: Edit(5, 5, nil, 1)   // then split at the end of its "b"
d1: <doc><p>a</p><p></p><p>b</p><p></p></doc>
d2: <doc><p>a</p><p>b</p><p></p><p></p></doc>
```

The §7.8 walk follows the InsNextID chain over unknown newer siblings and
splits the last of them at offset 0. On d2 the chain is `P → p3[b] → p4[]`;
p4 is d2's follow-up split of p3 at the end of "b", a later boundary. The
walk went on to p4, so d1's product landed after it; d1 resolves the
follow-up by position, after its own product.

## Plan

- [x] `TestTreeFurtherSplitAfterSameBoundarySplits` in
      `pkg/document/tree_split_order_test.go`: the issue's three scripts, a
      follow-up split at offset 0, three typed-into-the-product scripts, two
      with the right half deleted, four fuzz minima, the nested shape, a
      third replica in both arrival orders. 9 of 15 fail on `main`.
- [x] End the walk at the first sibling holding a child the editor's version
      vector knows (`holdsKnownChild`, descending into elements).
- [x] §7.8 in `docs/design/concurrent-merge-split.md`.
- [x] Revert the review round that changed §7.5's empty-run test and added
      a split-chain GC barrier (c86c81e3, aaaaadb1): Go-only, and §7.5's
      change diverged `TestTreeSplitAfterTypingAtSpanEnd`.
- [x] Pin the issue script's document, compare the third replica's two
      arrival orders, and keep the Style/RemoveStyle convergence test.
- [x] Take the split-chain GC barrier back out (it had returned in later
      fix rounds and kept gaining legs). A local barrier cannot cover a
      split applied after the purge; §7.8 now records what a purge can and
      cannot do to the marker, and `TestTreeSameBoundarySplitAfterGC` runs
      the splits with one replica having collected.

- [x] Scope the merge-moved skip to merges the editor had not seen
      (`mergeMovedConcurrently`). Bare `MergedFrom` presence is never cleared
      on live content, so any historical paragraph join blinded the §7.8
      marker for good and brought js#1433 back; §7.1 scopes the same field
      off the same immutable `MergedAt`.
- [x] Scan the chain node's own children even when an earlier chain step
      descended through it. The shared budget bounds the descent; it must not
      answer a chain step out of the cache.

## Verification

- [x] `go test ./pkg/document/... ./pkg/index/...`, `go vet`, `gofmt`.

## Merge gate (needs a human)

- [ ] **Do not merge before yorkie-js-sdk#1435.** `orderSameBoundarySplit` is
      a replicated convergence rule: a server carrying this change and a
      client without it (or the reverse) disagree on where a same-boundary
      split lands, and a snapshot built by either flips the other (measured
      on #2030 / js#1375). The two land together or not at all.
- [ ] yorkie-js-sdk#1435's `pnpm sdk test` against a server built from this
      branch. Needs a yorkie-js-sdk checkout and a running server, so CI here
      cannot tick it; a maintainer has to run it (or confirm js#1435 merged
      with the identical rule, including this round's `MergedAt` scoping and
      the always-scan-the-entry-node descent, which js#1435 must mirror).

## Out of scope

- yorkie-js-sdk#1436: text inserted at the position of a concurrent split
  lands on different sides. Different mechanism; left open by #2030.
- #2077 (the `KNOWN` skips in the same test file) and yorkie-js-sdk#1408.
- GC independence of the split rules. `holdsKnownChild` counts tombstones,
  and so do §7.4's re-parenting, the §7.5 advance and the §7.8 entry gate,
  which predate it. A purge can only clear the marker, which sends the walk
  where it went before this change, so it cannot break an edit set that
  converged without it (§7.8). Making the rules independent of GC needs a
  marker that does not live in tombstones, in both SDKs.

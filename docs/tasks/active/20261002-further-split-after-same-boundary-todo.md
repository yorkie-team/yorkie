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
      the splits with one replica having collected. (A narrower barrier came
      back later, for a reason that barrier never had: see below.)
- [x] Take the merge-moved skip back out, in both forms it took (bare
      `MergedFrom` presence, then `MergedAt` scoped to the editor's vector).
      A GC-free fuzz shows each diverges on more scripts than it fixes, the
      scoped form read a ticket that is first-move-only, copied onto split
      products, rebuilt from `removedAt` for older snapshots and unvalidated
      on element payloads, and js#1435 has no skip. `holdsKnownChild` now
      matches js#1435 exactly.
- [x] Scan the chain node's own children even when an earlier chain step
      descended through it. The shared budget bounds the descent; it must not
      answer a chain step out of the cache.
- [x] Settle whether GC can change `holdsKnownChild`'s answer, against the
      server's real ordering. A removal the editor had not seen cannot be
      collected before the split is applied (push before vector record, apply
      before collect). A removal it had seen can, and that case diverged
      (`TestTreeSameBoundarySplitUnderServerGC`). `Tree.PurgeHeldBack` keeps a
      tombstone while any ancestor is outside the min; §7.8 records the one
      case it cannot cover and the fuzz numbers.

## Verification

- [x] `go test ./pkg/document/... ./pkg/index/...`, `go vet`, `gofmt`.

## Merge gate (needs a human)

- [ ] **Do not merge before yorkie-js-sdk#1435.** `orderSameBoundarySplit` is
      a replicated convergence rule: a server carrying this change and a
      client without it (or the reverse) disagree on where a same-boundary
      split lands, and a snapshot built by either flips the other (measured
      on #2030 / js#1375). The two land together or not at all.
- [x] yorkie-js-sdk#1435's suite against a server built from this branch
      (js#1435 at 9a75fa42, server from 23c29150 on memdb, Node 22, pnpm 9,
      `vitest run` without `webhook_test.ts`, whose callbacks need Docker
      networking): 3372 passed, 1 failed. The failure is "gc targeting nodes
      made by deactivated client", where the server dialled its own default
      port 8080 while running on 18080; it is not the split rule. The rule
      itself is now the same in both: count every descendant the editor's
      vector covers, tombstones included, no merge skip.
- [ ] yorkie-js-sdk needs `PurgeHeldBack` too. It is a GC policy, not part of
      the replicated rule, so a JS client without it still agrees with a Go
      replica on where a split lands; but a JS client that collects such a
      tombstone early is exposed to the divergence the barrier closes here.
      Review round 4 narrowed the Go barrier to the ancestors §7.8 can
      actually land on (a live element inside an `InsNextID` chain, created
      by an actor the collecting vector names), so the exposure a JS replica
      carries is now that one shape rather than every tombstone in the tree,
      and `docs/design/garbage-collection.md` records the rule for both SDKs.

## Out of scope

- yorkie-js-sdk#1436: text inserted at the position of a concurrent split
  lands on different sides. Different mechanism; left open by #2030.
- #2077 (the `KNOWN` skips in the same test file) and yorkie-js-sdk#1408.
- GC independence of the other split rules. §7.4's re-parenting, the §7.5
  advance and the §7.8 entry gate read tombstones too and predate this
  change; the server-ordered fuzz shows `main` diverging with GC on 108 of
  100 000 seeds (62 with `PurgeHeldBack`). Making them independent of GC
  needs a marker that does not live in tombstones, in both SDKs.

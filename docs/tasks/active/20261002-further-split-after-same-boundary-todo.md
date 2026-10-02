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

## Verification

- [x] `go test ./pkg/document/... ./pkg/index/...`, `go vet`, `gofmt`.
- [ ] yorkie-js-sdk#1435's `pnpm sdk test` against a server built from this
      branch.

## Out of scope

- yorkie-js-sdk#1436: text inserted at the position of a concurrent split
  lands on different sides. Different mechanism; left open by #2030.
- #2077 (the `KNOWN` skips in the same test file) and yorkie-js-sdk#1408.

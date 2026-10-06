# Join paragraphs right after an Enter in the middle of a span

**Created**: 2026-10-06
Fixes #2147.

## Problem

Joining two paragraphs right after an Enter that split a span in the middle
is a no-op in Go, locally and when applied as a remote change. The JS SDK
merges them.

```go
// <doc><p><span>ab</span></p></doc>
tree.EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, nil, 1) // split the span
tree.EditByPath([]int{0, 1}, []int{0, 1}, nil, 1)       // split the paragraph
tree.EditByPath([]int{0, 1}, []int{1, 0}, nil, 0)       // join them again
// JS: <doc><p><span>a</span><span>b</span></p></doc>
// Go: <doc><p><span>a</span></p><p><span>b</span></p></doc>
```

Phase 3 (range narrowing) follows the first paragraph's last span along its
`InsNextID` chain to the split product in the second paragraph and starts
`collectBetween` there. The join's to position is the start of that
paragraph (`toLeft == toParent`), so the narrowed range runs backwards and
the merge is dropped. JS skips the narrowing in that case since
yorkie-js-sdk#1237 (v0.7.8); Go's Phase 3 dates from #1776 (v0.7.7) and never
got the guard.

An editor sends this sequence for Enter then Backspace, so a server's copy
of the document, and the snapshots it builds, keep the paragraphs apart
while every JS client has joined them.

## Plan

- [x] Red: `tree_join_after_split_test.go`, Enter as split level 2 and as
      span split + paragraph split, one or two spans, joined on the replica
      that split and on the other one. 8 of 8 fail on `main`.
- [x] Port the guard into Phase 3, moved into `narrowCollectRange` to keep
      `Edit` under the cyclomatic limit.
- [x] `TestTreeSplitReverseAfterRemoteSplitHistory` built its tree through
      this no-op, so its expected XML was Go-only (JS produces the tree this
      change produces). Move it to a scenario that does not join, with the
      same property: the reverse of the final split ends inside an emoji
      ([4,8)). It passes with and without the guard, and JS produces the
      same trees.
- [x] Phase 3 in `docs/design/concurrent-merge-split.md`.
- [x] Pin that the narrowing still applies away from a paragraph start
      (`TestTreeNarrowingAwayFromParagraphStart`).
- [x] `go test ./pkg/document/... ./api/...`, golangci-lint, `go fix -diff`,
      `go vet -tags integration ./test/...`.

## Known Limitations

- In 18 of about 40,000 compared races (self-review), JS rejects a remote
  change while building its reverse op on the post-edit tree ("index is out
  of range") and rolls it back, while Go applies it. With that error
  suppressed JS reaches the same state as Go. Both SDKs already lose content
  or diverge in those races; Go reaches that state only now that it merges.
- Not run: the MongoDB integration lane (no local MongoDB here).

# SplitText records the left piece's length in runes

**Created**: 2026-09-24

Reported as yorkie-team/yorkie#2027.

## Problem

`TreeNode.SplitText` in `pkg/document/crdt/tree.go` writes the index length of
the left piece in **runes**, while every other part of the tree counts **UTF-16
code units**:

```go
encoded := utf16.Encode([]rune(n.Value))
leftRune := utf16.Decode(encoded[0:offset])
...
n.Index.VisibleLength = len(leftRune)   // rune count
n.Index.TotalLength = len(leftRune)     // rune count
```

`TreeNode.Length()` is `len(utf16.Encode([]rune(n.Value)))` and `index.NewNode`
seeds both lengths from it, so a node's length — and every `TreeNodeID.Offset`
measured against it — is in UTF-16 code units. `len(leftRune)` undercounts by
one per surrogate pair, so a split whose left piece contains a non-BMP character
(emoji, flags, CJK extensions) leaves the node shorter than the positions that
anchor into it believe.

The reported document contained `"…한가위 되세요~🇰🇷ㅇㄹ"`; the flag is two regional
indicators (U+1F1F0 U+1F1F7), both non-BMP, so the node is 22 runes but 24
UTF-16 units. One `TreeEdit` whose `from` and `to` are the same position then
fails deterministically:

1. `FindTreeNodesWithSplitText(from)` splits at offset 22; the left piece spans
   UTF-16 `[0,22)` but records its length as 20.
2. `FindTreeNodesWithSplitText(to)` resolves the same anchor, follows
   `insPrevID` back to the left piece and re-splits it at 22. That is meant to
   return at the `offset == n.Len()` guard; with 20 it falls through to the
   range check and returns `ErrSplitOutOfRange`.

The stored ops are fine — only the replay is wrong — but the replay is
`BuildInternalDocForServerSeq`, so the document stops rebuilding: compaction in
housekeeping retries and fails every minute, and snapshot creation in `pushpull`,
`documents.GetDocument` and `revisions` fail whenever the document is not in the
snapshot cache.

The JS SDK is unaffected: `IndexTree.splitText` slices with `String.slice`, and
JS string length is already in UTF-16 code units. Go-server-only, which is why
clients converge while the server cannot rebuild.

## Plan

- [x] Failing tests (`pkg/document/crdt/tree_split_utf16_test.go`) over a text
      node holding a surrogate pair: the left piece's recorded length, and the
      same-anchor double split that the production document hit.
- [x] Record `offset` — which is by definition the UTF-16 length of
      `encoded[0:offset]` — instead of `len(leftRune)`.

## Review

- Targeted: `go test ./pkg/document/... ./pkg/index/...`.
- Not run here (needs MongoDB, left to CI): `make test`, `make test-complex`.
- No data migration: documents already stuck recover as soon as a server that
  can rebuild them replays their history.

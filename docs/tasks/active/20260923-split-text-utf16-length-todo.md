# SplitText records the left piece's length in runes

**Created**: 2026-09-23

Tracked as yorkie#2027.

## Problem

`TreeNode.SplitText` set the left piece's index lengths from `len(leftRune)`,
a rune count, while `Length()`, `index.NewNode` and every `TreeNodeID.Offset`
count UTF-16 code units. Text outside the BMP undercounts by one per surrogate
pair, and a caret edit resolves the same anchor twice: the second resolution
re-splits the left piece at an offset past its recorded end and fails with
`split offset out of range`.

On the server that failure repeats on every replay of the stored history, so a
document that took such an edit can no longer be rebuilt for a snapshot, a
compaction, or a load outside the snapshot cache.

## Measurement

Reproduced end to end with `@yorkie-js/sdk` 0.7.21 against yorkie 0.7.22: a
tree holding `"즐거운 한가위 🇰🇷ㅇㄹ"`, an insert right after the flag
(UTF-16 offset 12), then 520 changes so the server builds a snapshot. A client
attaching afterwards fails with

```
[internal] split 1:4:…:0 at 12 of 10: split offset out of range
```

The JS SDK is not affected on its own: its strings are UTF-16 already.

## Plan

- [x] Unit test on `SplitText` with `"a🇰🇷b"`: lengths in UTF-16 code units,
      and a second split at the left piece's end is a no-op.
- [x] Document test: the caret edit after the flag, then the history replayed
      through protobuf on a fresh document; and a caret edit at every
      non-surrogate offset keeps each piece's length equal to `Length()`.
- [x] Record `offset` — the left piece's length in UTF-16 code units.

## Review

- New tests fail on main (`split … at 12 of 10`) and pass.
- `pkg/...` 21 packages, `test/integration` (all), `test/complex -run Tree`
  1612 pass, `golangci-lint` v2.11.4 0 issues.
- The same end-to-end reproduction against a server built from this branch:
  the late client receives a snapshot and matches the writer.
- Stored documents recover with the fix: snapshots do not carry index lengths
  (they are recomputed from `Length()` on load), and the replayed split now
  records the right one.

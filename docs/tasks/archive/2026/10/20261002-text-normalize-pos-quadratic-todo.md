# Text: local edits went quadratic after the undo/redo port

**Created**: 2026-10-02

## Problem

Since `36235fd4` (#1932, the undo/redo port) every local `Text.Edit` builds a
reverse operation, and building it calls `RGATreeSplit.normalizePos`. That
function walks the whole physical `prev` chain back to the head, summing
`node.Len()` -- and `TextValue.Len()` allocates a fresh UTF-16 slice per call.
A local edit is therefore O(n) in the number of split nodes, with n
allocations, and n local edits are O(n^2).

The CI bench history (`yorkie-ci-benchmark` branch) pins it to that commit:

| main commit | `BenchmarkTextEditing` | allocs/op | bench job |
|---|---|---|---|
| `5a18e335` (v0.7.16) | 6.1 s | 18.7 M | ~10 min |
| `36235fd4` (#1932)   | 294 s | 9.18 B | ~21-25 min |

`DocumentDeletion/single_text_delete_all_100000` regressed the same way, but
invisibly: its 100k single-character inserts run with the timer stopped, so
ns/op barely moved while the wall-clock grew to ~6 minutes.

Remote replay is not affected (`OpSourceReplay` skips the reverse), so the
server's snapshot/compaction paths are fine; Go clients editing locally are.

## Plan

- [x] Reproduce: measure the local-insert cost growth on `main` (Red)
- [x] Property test: the new `normalizePos` equals the linear walk across
      random insert/delete/style/undo/redo/GC sequences
- [x] `normalizePos`: take the offset from `treeByIndex.IndexOf` (O(log n)).
      No tombstone special case is needed: `DeleteRange` keeps removed nodes
      in the splay tree at weight zero, and only `Purge` -- which also unlinks
      the node from the chain -- takes one out
- [x] `TextValue.Len`: count UTF-16 units without allocating
- [x] Verify: Red -> Green on the reproduction, `make verify`, bench numbers
- [ ] Self review, PR

## Review

Measured on an M-series laptop (`go test -tags bench`):

| benchmark | main | this branch |
|---|---|---|
| `BenchmarkDocument/text_1000` | 9.8 ms, 553 k allocs | 1.7 ms, 32 k allocs |
| `BenchmarkDocument/text_10000` | 781 ms, 50.5 M allocs | 19 ms, 320 k allocs |
| `DocumentDeletion/single_text_delete_range_1000` | 14.9 ms, 579 k allocs | 6.1 ms, 45 k allocs |
| `BenchmarkTextEditing` | (CI: 206 s, 9.18 B allocs) | 2.6 s, 12.9 M allocs |

`text_10000` / `text_1000` went from 80x to 11.5x: linear again.
`BenchmarkTextEditing` allocates less than before the regression (18.7 M).

Gates: `make verify` green; `go test -tags integration -race ./...` green
against local MongoDB.

Self review, round 1 (correctness/tests): no blocking findings. The reviewer
checked the splay invariant on every path (local/remote edit, split,
restore/retombstone, Purge, DeepCopy, snapshot decode) and ran a scratch
two-replica harness comparing NormalizePos to the chain walk on the root, a
DeepCopy and a bytes round-trip -- all equal, and final documents
byte-identical to the old code. Non-blocking findings and what was done:

- The new test spent 16 s building failure messages on passing checks --
  fixed, now 0.14 s.
- Comments in `document.go` and `operations/edit.go` still described the
  linear walk -- updated.
- The test is single-replica, so remote deletes, DeepCopy and snapshot
  decode are covered only by the reviewer's scratch harness -- left as a
  known limitation.
- The scratch harness saw two replicas diverge under undo/redo plus
  concurrent edits on some seeds, identically on old and new code -- so not
  caused by this change; root cause not established. Worth its own task.

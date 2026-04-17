---
updated: 2026-04-17
---

# Archived Tasks

## 2026/04

- [StyleByPath range support (Go SDK)](2026/04/20260402-style-by-path-range-todo.md): Add range signatures to Tree.StyleByPath / RemoveStyleByPath (JS SDK lives in a separate repo)
- [Concurrent Merge/Split convergence fixes](2026/04/20260406-concurrent-merge-split-todo.md): Fixes 1–10 complete; all 10 concurrent merge/split integration tests pass; 63/63 convergence cases covered
- [Merged fields snapshot encoding](2026/04/20260410-merged-fields-snapshot-todo.md): Fix the latent convergence bug where TreeNode merge runtime fields were dropped across snapshot roundtrip ([lessons](2026/04/20260410-merged-fields-snapshot-lessons.md))
- [TreeStyle split divergence fix](2026/04/20260410-tree-style-split-divergence-todo.md): Fix TreeStyle divergence on concurrent split with End-token split sibling guard ([lessons](2026/04/20260410-tree-style-split-divergence-lessons.md))
- [Epoch-aware push](2026/04/20260417-epoch-aware-push-todo.md): Add epoch check to pushPack to discard stale-epoch changes before DB/cache insertion
- [Counter dedup](2026/04/20260413-counter-dedup-todo.md): Counter deduplication task

## 2026/03

- [Document Epoch](2026/03/20260327-document-epoch-todo.md): Epoch-based compaction with client-side detection and recovery

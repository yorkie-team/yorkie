**Created**: 2026-10-13

# A peer's Tree undo leaves pending undo entries on stale indices

## Problem

yorkie#2222. When a peer's identity-preserving Tree undo/redo (an op carrying
`restoreSpans`/`retombstoneSpans`) arrives, `Document.applyChanges` reconciles
the local undo/redo stacks with `TreeEdit.NormalizePos` and `GetContentSize`.
That path returns before either is captured, so it reported `(0, 0, 0)` and
no stacked entry moved, although visible nodes left or came back. A pending
index-addressed entry (a split's boundary deletion, a merge's re-split) then
ran at the wrong index: undoing an Enter produced
`<doc><p><span>ab</span><span>efgh</span></p>cd</doc>` instead of
`<doc><p><span>abcd</span></p><p><span>efgh</span></p></doc>`.

JS fixed the same gap in yorkie-js-sdk#1421 (`getExecutedRanges`).

## Options explored

| Option | Verdict |
|---|---|
| Per-node visible edits from `Tree.Restore`/`Retombstone` (JS #1421 port) | Chosen: matches JS exactly, passes every scenario below |
| Measure in the operations layer, no crdt signature change | Has to copy `isolateTextRange`'s piece arithmetic; more fragile, no smaller |
| One envelope `(from, to, size)` per op | Wrong: a restore of runs on both sides of a live node needs a different shift at each gap (`TestUndoSplitBetweenNonContiguousRestoredNodes`) |
| Rebase stacked entries through `CRDTTreePos` on receive | Rejected earlier in `docs/design/undo-redo.md` (a slot, not a node) |
| Identity-addressed split reverses | Proto change in both SDKs; merge reverse still needs indices |

## Plan

- [x] Reproduce the reported case (Red)
- [x] `Tree.Restore`/`Retombstone` return one `TreeVisibleEdit` per node that
      came back or left, measured at that moment, for remote executions only
- [x] `TreeEdit.ExecutedRanges` and the reconcile loop iterate it
- [x] 31-scenario matrix whose expected strings come from JS main
      (retombstone/restore/redo, text/element, multi-node, three replicas)
- [x] Non-contiguous counterexample that an envelope would fail
- [x] `make lint`, `go test ./pkg/... ./api/...`, integration Tree/History
- [x] Update the reconciliation section of `docs/design/undo-redo.md`

## Review

- Red on main: 28 failing (sub)tests across the three new test files; Green
  with the fix. Unit 1402 passed, integration (Tree|History|Undo|Redo) 581
  passed.
- Only `OpSourceRemote` executions measure: the reconcile loop reads
  nothing else. The server rebuilds through `OpSourceReplay`, and local
  undo/redo no longer pays index walks whose result is thrown away.
- Known limitation: a receiver with empty history still measures (as JS
  does), and a remote op is measured twice, on the clone and the root;
  skipping either would need a "will reconcile" flag plumbed into Execute.
- Not ported from #1421: Tree OpInfo paths/values and the event-gate change.
  Go's identity path already reports `Observable: true`.

## Follow-ups (not part of this fix, also reproduce in JS)

- A forward delete across a concurrent split reports
  `[PreEditFromIdx, PreEditFromIdx+RemovedSize)`, which swallows the pending
  boundary between the two removed runs.
- A retombstone racing a concurrent element split leaves the split-born `<p>`
  on one replica only.
- A restore under a paragraph that a peer later revives stays removed on one
  replica (`<p>ad</p>` vs `<p>abcd</p>`).

**Created**: 2026-10-12

# Leave a restore span with a text parent unplaced

Fixes yorkie#2168. The JS half is yorkie-js-sdk#1471.

## Problem

`Tree.recreateFromSpan` checks only that a span's `ParentID` names an
existing node. When it names a text node, every anchor rung misses and the
id-order fallback calls `parent.InsertAt`, which returns
`ErrInvalidMethodCallForTextNode`. The whole change then fails to apply.

Restore spans come from the wire. The server stores a pushed change before
it applies it (`CreateChangeInfos` runs ahead of
`BuildInternalDocForServerSeq`), so one crafted `TreeEdit` from any client
with write access makes every later snapshot rebuild of that document fail.
Every Go replica that pulls the change fails the same way.

## Layer decision

- **Decode time cannot decide it.** Whether `ParentID` names a text node
  depends on document state. `fromTreeRestoreSpans` sees only the span.
- **Push-time validation would not help stored changes.** A change already
  in the database still has to apply on every rebuild.
- **Apply time, deterministic skip.** Treat a non-element parent exactly
  like a missing one (B1): leave the node unplaced and return no error.
  Node kind is immutable and the parent ID resolves to the same node on
  every replica (or to nothing, which is already B1), so every replica
  skips the same span and they converge. This also fixes stored changes.

## Plan

- [x] Read issue, JS mirror, #2119, #2081 pattern, `recreateFromSpan`
- [x] RED: document-level test. A crafted restore `TreeEdit` (round-tripped
      through the converter) whose span names a text node as parent must
      apply on a receiving replica without error and leave the tree as is
- [x] RED: crdt-level test on `Tree.Restore` for an element span and a text
      span under a text parent
- [x] Run the tests on unmodified code and record the failure in lessons
- [x] Fix `recreateFromSpan`: skip when the parent is a text node
- [x] GREEN, plus a test that an honest undo/redo after GC still recreates
      the node through the restore path (`honest undo after GC` subtest)
- [x] Update `docs/design` where B1 is described
- [x] `make verify`; relevant integration tests
- [x] Commit

## Review

- `recreateFromSpan` now returns `(nil, nil)` when the parent is a text
  node, the same result as a missing parent. One check, no new error.
- Tests: `TestRestoreSpanUnderTextParentStaysApplicable` in
  `pkg/document` (pull, server rebuild, convergence, editability, honest
  undo after GC) and `TestTreeRestoreTextParentSkip` in `pkg/document/crdt`.
  Both failed on `main` with `text node cannot have children` and pass now.
- `make verify` green. `go test -tags integration ./test/integration/...
  -run 'TestHistoryTree|TestTree'` green against local MongoDB.
- `docs/design/undo-redo.md` states the parent rule.
- JS mirror (yorkie-js-sdk#1471): in `CRDTTree.recreateFromSpan`, return
  `undefined` when the resolved parent `isText`, right after the existing
  parent-missing check.

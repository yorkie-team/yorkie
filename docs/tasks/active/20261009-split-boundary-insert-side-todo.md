# Port the split-boundary insert-side rule from the JS SDK

**Created**: 2026-10-09

yorkie-js-sdk#1467 (head `89b0b2a8`, fixes yorkie-js-sdk#1436) changes where
a same-boundary split and an insert at that boundary land. Where a split lands
is a replicated contract, so the Go tree has to make the same decisions or a
mixed JS/Go fleet diverges. Design:
[split-boundary-insert-side](../../design/split-boundary-insert-side.md).

## Plan

- [x] Translate the five cases of `tree_boundary_insert_side_test.ts` into
      `pkg/document/tree_boundary_insert_side_test.go` (XML and node-ID shape)
- [x] Red on `main`: four of five fail (the typist-is-replica-1 case already
      converges on `main`)
- [x] Port `orderSameBoundarySplit`'s boundary-insert run (`boundaryInsertRunOf`)
- [x] Port `advanceIntoSplitProducts` / `atEndOfLiveContent` and apply it to a
      collapsed range in `Edit` after Phase 1
- [x] Green: all five pass; `go test ./pkg/document/...` passes
- [x] Scenario for review finding (a): trailing child removed concurrently,
      both delivery orders, concurrent split right after it
- [x] Design doc, README index, §7.8 cross-reference
- [x] `go build ./...`, `make lint`

## Review

- Finding (a) reproduces: with the removal applied before the insert, the
  insert crosses the removed child into the split product
  (`<p>a</p><p>rub</p>`) while the typist keeps it in the paragraph
  (`<p>au</p><p>rb</p>`). JS at `89b0b2a8` diverges identically (checked by
  running the same scenario in a JS worktree); the base of both converges.
  Kept as a skipped subtest; the fix belongs to both SDKs together.
- Finding (b) (`orderSameBoundarySplit` has no live-content rule) is ported
  as is and not separately tested.
- The JS PR also moves where the split's `TreeChange` position is measured;
  the Go tree emits no such change, so nothing was ported for it.

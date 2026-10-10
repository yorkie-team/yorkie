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

## Round 2: order-independent gates (2026-10-10)

Review of yorkie-js-sdk#1467 found three gates that read replica-local state.
The JS PR takes the same three fixes; this round mirrors them.

- [x] `atEndOfLiveContent` counts a trailing child as gone only when its
      removal is known to the change (`removedAt` + `TicketKnown`); pass the
      version vector into `advanceIntoSplitProducts`
- [x] `movedBySplit`: both run measurements stop at the first child younger
      than the split product
- [x] `orderSameBoundarySplit` redirects into the next product only when it
      is adjacent (target is parent, or holds nothing past its run)
- [x] Un-skip scenario (a); add Enter-then-type (two and three replicas) and
      the three-split case, test names aligned with the JS suite
- [x] Port the seven fuzz minima of the remaining #1436 cases as skipped
      subtests; confirm each fails in Go before skipping
- [x] Compare every case's Go replica states with the JS ones
- [x] Design doc: order-independent gates, remaining divergences
- [x] `go build ./...`, `go test ./...`, `make lint`

### Review

- All seven remaining cases diverge in Go and end in the same two states as
  JS, node IDs included (Go prints a different delimiter number, the
  structure is identical). None was dropped.
- Seeds 101, 235, 193 and 24 diverge on `main`, at `89b0b2a8` and with the
  same-boundary walk fix (yorkie#2098 / yorkie-js-sdk#1435, both still open)
  applied, so they do not depend on that fix.
- Seeds 69, 502 and 3768 come from `movedBySplit`: disabling it makes them
  converge and breaks Enter-then-type. Seed 3768 converges on `main`, so it
  is a node-ID-order regression the filter trades for Enter-then-type.

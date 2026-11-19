# Lessons: keep a release record zero when nothing will collect the charge

## The filed location was one caller off

The issue pointed at `moveSizeToGC` and named a guard
(`ChargeToRemovedElement`) that does not exist in `main` — the function it
quotes is `AccMovedElement`, from #2037, and it carries no guard at all. The
described defect is real, but it belongs to that function, not to
`moveSizeToGC`.

What separates the two is not the top-up, it is what stands behind it. A
top-up is only residue when no collection entry covers the element afterwards.
`moveSizeToGC`'s three callers all register one (or are covered by an
ancestor's `Descendants` walk in `deregisterElement`); `AccMovedElement`
registers nothing, because a move is not a removal. Reading "tops up a zero
record" as the defect would have produced a change that broke the balanced
path and left the unbalanced one alone.

Generalisation worth keeping: in this accounting code the question is never
"does this write a charge?" but "who subtracts it, and can they still reach
it?". `release` is the one operation that answers *nobody* — and that is
exactly what the zero record encodes.

## The test had to fail for the right number

The regression is 24 bytes — one ticket — in `docSize.GC`, against a root
whose rebuild has none. Nothing exported shows it: `GarbageLen` is zero either
way, because the residue has no pair, and the document marshals identically.
`assertRebuildsSame` is what sees it, which is why the existing fixture was
the right place to put the test rather than a new one at the `json`/`document`
layer.

Confirmed by reverting only `root.go` and re-running: before the guard,
`GC.Meta` is 24 and the rebuild's is 0.

## Round log

- **Verification**: `make lint` (0 issues), `make verify` green,
  `go test ./pkg/document/... -count=1` green. `/self-review` was not run —
  this was an autonomous one-shot run with no reviewer subagent available, so
  review is CI plus the PR's human reviewers.

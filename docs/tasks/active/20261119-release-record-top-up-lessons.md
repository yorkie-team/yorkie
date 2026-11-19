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

## Round log

(filled in as review rounds run)

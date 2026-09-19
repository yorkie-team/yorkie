**Created**: 2026-09-19

# Lessons: reporting a split's boundary growth

Captured while fixing yorkie#1999. The plan is in
`20260919-split-invisible-to-undo-reconcile-todo.md`.

## The issue's cause was wrong, and the wrong cause pointed at the wrong code

#1999 reasoned that the reverse of a split is a merge, and that a second
boundary inside the same node makes that merge resolve over a range wider than
the split opened. Plausible, and it names the merge as the suspect.

The merge is correct. The range is never widened — it is never *shifted*. A
split reports no inserted content and no removed range, so a remote split
reaches reconciliation as a zero-width, zero-growth edit and a stacked reverse
to its right keeps pre-remote indices, which in the settled tree name live
characters. Two facts fall out immediately once that is the framing and were
invisible under the issue's: undoing the *first* split is fine because the
other is to its right (Case 2, no shift needed), and two *sequential* splits on
one replica undo correctly because nothing arrives remotely at all. Both are
one-line tests, and either would have refuted the stated hypothesis in a
minute.

Reproduce before adopting the reporter's mechanism, even when — especially
when — the reporter is you.

## Making a cached value load-bearing is a change in its own right

`Root().Len()` had no correctness-critical reader before this. Reporting the
split's growth off it gave it one, and immediately surfaced that
`SplitElement` inflated it: a piece born tombstoned lengthened live ancestors
that nothing could ever drain. Guarding that then surfaced a second — §7.4's
re-parenting used `DetachChild`/`InsertBefore`, neither tombstone-aware, and
the first bug had been cancelling the source side of the second by accident.

Two latent errors cancelling to zero is not a stable state; it is a trap armed
for whoever reads the value next. A fuzz over cached-length-versus-traversal
put numbers on it: 313 of 3000 seeds violate on `main`, 0 with both repairs.
When a fix starts reading state nothing read before, budget for auditing that
state, not just for the fix.

## Measure what happened; do not recompute what was asked for

`2*splitLevel` is what the caller requested. The split loop stops when it runs
out of ancestors, and a split product can be born tombstoned, so the number of
boundaries actually opened is a different quantity. Reading it off the tree
covers both without enumerating either.

The same assumption was wrong in a second place, and finding it took a
reviewer rather than the fix: `toSplitReverseOperation` sized its own reverse
the same way, so a lone `Edit(3, 3, nil, 3)` on a two-level tree undid to
`ab0123456789`. No concurrency, no history — a single local edit losing text.
Introducing the measured value and then not using it where the same assumption
lives is the inconsistency to look for after any fix of this shape.

## In-memory replica helpers are not a wire

`crossSync` hands operation *pointers* between two documents. An undo
re-derives its range from integer indices on every execution, and those
indices are local-only state that does not cross the wire — so the same
scenario can behave differently in memory and over protobuf, and a reviewer
demonstrated exactly that. The convergence a helper like this asserts is
weaker than it looks. Anything touching undo reconciliation needs at least one
case forced through a real encode/decode.

## Say which hunk each test pins

Six subtests, three hunks, and a reviewer's per-hunk revert table showed that
two subtests passed pre-fix rather than the one assumed — and that a case whose
comment reads as a no-shift assertion is the sole pin for an entirely different
guard. A test that fails for the diff as a whole does not tell you which part
it protects. Reverting hunks one at a time is cheap and is the only way to
know.

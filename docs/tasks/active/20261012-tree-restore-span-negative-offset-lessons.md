# Lessons: Tree restore span with a negative identity offset

**Created**: 2026-10-12

## The codebase had already written down which layer the fix belongs to

The issue offered two remedies — "the same strictness the anchor path already
applies, or an explicit skip in Restore" — and the comment block above
`fromTreeRestoreAnchorID` had already argued the first one out of existence for
this exact decoder: it runs over stored operations too, so a rejection turns a
once-poisoned document into a permanently unloadable one, and flooring an
identity (unlike an anchor, which resolves by position) silently aliases two
nodes. Reading the rationale attached to the neighbouring guard, rather than
just its code, decided the design in one step.

The corollary is that `Length < 0` *is* rejected in the same converter function,
which looks like a contradiction until you notice what each value does: a bad
`Length` slices `Value` out of bounds inside the decoder's own blast radius,
while a bad offset is a semantic claim about identity that the CRDT layer is the
only one able to adjudicate. Same file, same struct, different layer — the
distinction is memory safety versus convergence.

## A guard can answer "clean" rather than be absent

The interesting part of the bug is not that no check exists — `recreateParent`
does probe for a node already holding the id. It is what the probe returns.
`findFloorNode` keeps a floor only when it shares the probe's `createdAt`, and
every node of an insertion sits at a non-negative offset, so the floor for a
negative one can only land on an *earlier* insertion, is discarded, and comes
back nil. "No surviving node holds this id" is a true answer to the question as
asked and the wrong answer to the question that matters. Worth remembering when
auditing: an input outside a predicate's assumed domain does not necessarily
make the predicate fail — it can make it report the all-clear.

The first write-up of this lesson got the mechanism wrong, claiming the probe
returned an earlier insertion's node and that the interval test was then
vacuously true. Reading `findFloorNode`'s `CreatedAt.Compare(...) != 0 → nil`
rather than reasoning from the ordering alone would have caught it. The bug and
the fix are unchanged either way, which is exactly why the error survived a
round: a rationale that lands on the right remedy is not thereby verified.

## An apply-time skip is half a fix for crafted input

Review round 1 (security lens) landed on the layer, not the predicate: a skip in
`Restore`/`Retombstone` makes this replica immune and leaves the change itself
valid, so the server persists it and fans it out to every peer. Any replica
without the same rule — today, every JS one — still acts on it. For an input
that only ever arrives crafted, the question "does my replica handle it" is the
smaller one; "does it get to exist in the log" is the one that bounds the blast
radius.

The reason the original approach ruled out a wire rejection was that
`fromTreeRestoreSpans` also decodes stored operations, which is true and is why
a bare rejection is wrong there. What it missed is that the codebase already
solved that conflict generally: `NormalizeStoredOperations` repairs, on the
stored path only, exactly the shapes the wire path rejects (`SplitLevel < 0` is
the closest precedent, and `Length < 0` is rejected on a restore span already).
"This decoder serves two paths" is a reason to pair a rejection with a repair,
not a reason to skip instead. Both files sit outside this change's diff, so the
ingress half is recorded as its own checklist item rather than folded in here.

Round 2 (design-fit and security lenses) reached the same conclusion from two
directions: the layer, and the Go-vs-JS divergence the apply-time half opens.
Neither half is reachable from `pkg/document/crdt` — `fromTreeRestoreSpans` and
`NormalizeStoredOperations` are in `api/converter`, and the mirror is in another
repository. The one option that *was* in reach, raising the skip to an error out
of `Restore`/`Retombstone`, would reject the change at `Execute` and so stop the
persist-and-fan-out — but at the cost the skip was chosen to avoid: a document
already holding such a span would fail every later rebuild, with its repair
(`NormalizeStoredOperations`) sitting on the far side of the same boundary. A
fix that converts one defect into another is not a fix. What belongs here
instead is the write-up: the rule, and what it leaves open, now sit in
`docs/design/undo-redo.md` beside the sibling skip rules rather than in a code
comment and this file alone.

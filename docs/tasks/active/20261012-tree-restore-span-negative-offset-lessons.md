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

## A guard can be vacuously true rather than absent

The interesting part of the bug is not that no check exists — `recreateParent`
does probe for a node already holding the id. It is that with a negative offset
the probe lands on an unrelated earlier insertion, where `holder.id.Offset ==
id.Offset` is false and `holder.id.Offset + holder.Length() > id.Offset` is true
for *every* text node, because every honest offset exceeds `-1`. The check runs,
answers, and means nothing. Worth remembering when auditing: an input outside a
predicate's assumed domain can make it return a confident wrong answer, which is
harder to spot in review than a missing call.

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

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

# Lessons: Tree splitting edit loses its undo entry

**Created**: 2026-08-16

## "Keep the stack aligned" is not the same as "fix the bug"

The obvious-looking remedy — push a no-op sentinel so the undo stack keeps one
entry per user edit — reads like a fix and is not one. Tracing it press by
press showed the scrambled document state simply arrives on the second `Undo`
instead of the first: the wrong edit is still reverted and the split is still
applied. What the sentinel actually buys is a dead keypress.

The general form: when a proposed fix targets a *symptom's shape* (stack depth
misaligned) rather than its *cause* (no reverse operation exists for this
edit), simulate the full user-visible sequence before accepting it. If the bad
end state still occurs, the fix is cosmetic, and a cosmetic change that also
diverges from the SDK you are porting from is worse than doing nothing.

## The parity rule needs its justification restated per case, not assumed

The port's rule is "reproduce JS's defects, don't fix them one-sidedly." The
strongest supporting argument on this branch — *Go is also the server, so a
one-sided fix means permanent server-versus-client divergence* — was carried
over from the Tree Style filing and did not actually apply here: undo/redo
stacks never leave the client. Noticing that mattered, because it meant the
decision had to stand on the weaker, general argument (drift between SDKs) and
therefore had to be argued rather than asserted.

Reusing a decision's *conclusion* across cases is cheap; reusing its
*reasoning* without checking that the premises still hold is how a rule turns
into a reflex.

## An undocumented limitation in brand-new public API is its own defect

`Document.Undo` is new public Go API. "Faithful to JS" answers whether to
change the behavior; it does not answer whether to tell anyone. A defect that
is (a) reachable from ordinary single-client editing, (b) silent — no error,
`CanUndo()` still true — and (c) destructive of the user's content needs a
written home even when the code is deliberately left alone. Filing it and
adding the Risks row were not paperwork; they were the deliverable, since the
code change was correctly zero.

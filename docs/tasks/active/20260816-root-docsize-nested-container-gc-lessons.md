# Lessons: DocSize's GC total goes negative on nested-container removal

**Created**: 2026-08-16

## A number appearing in a fix's RED output is not evidence it caused that number

`TestHistorySkippedUndo`'s RED output (before Critical 1's fix, on this
branch) showed `DocSize{Live:{Data:2, Meta:72}, GC:{Data:-2, Meta:-48}}` as
the *expected* baseline value — the value d1 should have converged to. It
read, at a glance, like evidence that the skipped-operation bug being fixed
was what produced the negative `GC`. It was not: that figure was the
baseline, computed independently of the fix, and it was already wrong
because of this unrelated bug in `Root.RegisterRemovedElementPair` /
`deregisterElement`. The fix's actual effect was on the *actual* value
converging to the (already-wrong) expected one, not on the sign.

The general form: when a regression test's failure output contains a
striking anomaly (a negative size, an off-by-huge-amount number), don't
assume the change under test produced it. Trace where each side of the
assertion — expected and actual — is computed, independently, before
attributing the anomaly to either.

## Reproduce before writing the filing, even when the number is already given

The task handing off this bug supplied the exact reproduction and expected
output. It would have been faster to transcribe it. Running it anyway (a
four-line scratch test against this checkout, deleted afterward) was the
difference between "the report says X" and "I saw X" — and it caught that
the code path involved (`Object.Delete` → `RegisterRemovedElementPair` →
later `GarbageCollect` → `deregisterElement`) really is a pre-existing,
main-identical path, not something the port's own changes route through
differently. Confirming the *mechanism*, not just the *number*, is what
makes the "pre-existing on main" claim defensible rather than asserted.

## An asymmetric add/subtract pair is a size-accounting bug shape worth naming

`RegisterRemovedElementPair` walks one level (the removed element only);
`deregisterElement` walks the full descendant tree. Any bookkeeping pair
where one direction aggregates over a structure and the reverse direction
does not is a latent underflow/overflow bug, independent of what the
structure represents. Worth a general heuristic when reviewing GC or
refcount-style code: for every "add on remove" site, find its "subtract on
purge" counterpart and check they walk the same set of nodes.

## See Also

- `docs/tasks/active/20260816-root-docsize-nested-container-gc-todo.md` —
  the paired todo with full mechanism and reproduction

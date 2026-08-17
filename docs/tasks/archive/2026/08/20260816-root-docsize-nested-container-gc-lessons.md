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

## Closing an asymmetry amplifies whatever double-counts next to it

Making `RegisterRemovedElementPair` walk descendants was the obvious fix,
and on its own it made adjacent bugs measurably worse. A concurrent remove
can call that function twice for the same element (`ElementRHTNode.Remove`
returns true again when a later ticket wins LWW); a member can be removed
remotely *after* its container was removed locally. Before the walk, each of
those double-booked one element's size. After it, they double-booked whole
subtrees — one case ended up strictly worse than `main`, which had been
accidentally correct there.

The bug was pre-existing either way, but "my change made an existing bug
bigger" is the same user-visible regression as "my change introduced a bug".
The habit worth keeping: after widening what a bookkeeping function does,
enumerate how many times it can be called for the same subject and from
which directions, and *run* each of those rather than reasoning about them.
Every one of the three additional manifestations here was found by running a
scenario, not by reading the diff.

## Two guards in a row meant the invariant was still unnamed

The first attempt was the descendant walk. The second added an idempotence
guard keyed on `gcElementPairMap`. The third had to add a second, different
guard, and that was the signal: each guard was patching a symptom of a rule
nobody had written down. Once it was written down —

> every registered element's size is counted in exactly one of Live or GC,
> and one map says which, and how much

— the three call sites each had one obvious job, the guards collapsed into a
single idempotent `moveSizeToGC`, and the fourth manifestation (an element
created inside an existing tombstone, which no descendant walk can reach
because it does not exist when the walk runs) was fixed by the same rule
without needing its own special case.

Reaching for a third guard in the same function is a good moment to stop
and ask what property the guards are collectively defending.

## A booked amount beats a boolean when the quantity is not stable

`DataSize()` grows by one ticket the moment `removedAt` is set — so an
element's size can change *after* that size has been charged to GC, if the
element is removed remotely inside a container that was already removed
locally. A `map[key]bool` of "this is in GC" is then not enough: the
subtract side reads today's `DataSize()` while the add side charged
yesterday's, and the difference lands in GC forever.

Storing the charged `DataSize` instead makes the two sides symmetric by
construction, independent of when either side reads the element. The general
shape: when a ledger's entries are computed from mutable state, record what
was posted, not that something was posted.

## The same bug's numbers in two SDKs is the strongest parity evidence

The JS SDK reproduces this identically — `registerRemovedElement`
(`crdt/root.ts:257`) has the same missing descendant walk. What made that
finding solid was not reading the two functions side by side but running the
same four scenarios through both SDKs and getting byte-identical figures:
object `gc:{-2,-48}`, array `gc:{-2,-24}`, nested object `gc:{-2,-96}`, text
clean in both. Identical *code shape* invites the reply "but the surrounding
paths differ"; identical *numbers* from independently executed code does
not.

The one place the SDKs did diverge — Go skipping the second registration
when a remove loses LWW, JS registering unconditionally — was invisible in
the source shape and only appeared because the concurrent scenario was run
on both.

## See Also

- `docs/tasks/archive/2026/08/20260816-root-docsize-nested-container-gc-todo.md` —
  the paired todo with full mechanism and reproduction

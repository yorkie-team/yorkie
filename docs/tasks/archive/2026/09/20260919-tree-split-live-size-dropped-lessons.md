**Created**: 2026-09-19

# Lessons: charging Live only for the sizes it was holding

Captured while fixing yorkie#1998. The plan is in
`20260919-tree-split-live-size-dropped-todo.md`.

## The issue's expected value was wrong, and chasing it would have hidden the bug

#1998 states that a split and the merge that undoes it leave the tree exactly
as it was, so the live size should come back to where it started. The tree is
identical; the *node set* is not. A CRDT text split is permanent, so the merge
rejoins the element and leaves two live text nodes where there was one. The
steady state is one ticket above the pre-split size, forever, and that charge
is correct.

Had the fix been written to satisfy the issue's figure, it would have had to
subtract a ticket somewhere that legitimately holds one — trading an unbounded
drift for a constant error, and burying the second bug under it. The reported
symptom was real; the reported expectation was not. Both needed measuring.

## A function whose signature has nowhere to put a value will drop it

`(*Tree).split` was declared `) error`. `(*TreeNode).Split` returned a
`DataSize` the whole time, and every other caller propagated it. The one
caller that could not was the one whose signature had no slot — and the
compiler is happy, because `_` is a valid thing to write.

The same shape showed up twice more in the same file: `diff = diff2` instead
of `diff.Add(diff2)` in `FindTreeNodesWithSplitText`, safe only because the
variable is provably zero at that point. Assign-versus-accumulate on an
out-parameter is invisible in review and invisible in tests that exercise one
iteration. A level 2 case is what makes the difference observable at all, and
the level 1 test passed with the loop's accumulation mutated away.

## `DeepCopy().DocSize() == DocSize()` is worth more than any constant

The incremental ledger and the one `NewRoot` recomputes from the tree are two
independent implementations of the same number. Asserting they agree catches
this entire class of bug without knowing what the right constant is, and it
caught the defect from the other side before the cycle test did: after a
single split the live document said `{20 192}` and a rebuild said `{20 216}`.

Every size test in this repo pins exact constants, which is right for
documenting the model — but no tree-split test carried the agreement
assertion, which is why an unbounded drift survived. Both belong: the constant
says what the size is, the agreement says the ledger is consistent.

## "Live may only be debited for a size it was holding" is one rule, not two

The split half and the attribute half read as unrelated bugs — one in Phase 7,
one in the RHT — and were found weeks apart in the same review. They are the
same sentence. `RHT.Remove` mints a tombstone for a key that was never set, so
that a remove arriving before its set still wins; that tombstone replaces
nothing live, and debiting for it is the identical mistake as crediting
nothing for a split's new element.

Stating the rule that way is what made the second fix small: the repair is not
new accounting, it is routing those pairs through `GCOnlySize`, which already
existed for born-tombstoned split pieces. The mechanism was there; only the
recognition was missing.

## Negative is a different failure from wrong

A size that is too small by a constant is a bug. A size that walks down
without bound crosses zero, and past zero `MaxSizeLimit` stops applying — the
document is no longer bounded at all. The attribute toggle reaches
`Total() = -211638` in 2000 cycles, which is a rich-text editor toggling bold
for a few minutes. Worth separating in a bug report: unbounded drift on a
number that gates a limit is a availability problem, not an accounting nit.

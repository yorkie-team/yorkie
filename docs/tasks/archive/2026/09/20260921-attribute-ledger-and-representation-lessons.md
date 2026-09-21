# Lessons — the attribute ledger, and which representation is canonical

**Created**: 2026-09-21

## The plan file should have come first, and did not

The workflow says write the todo before touching code. This task went
straight to a reproduction instead, and the todo was written retrospectively
after the PRs were open. Nothing went wrong because of it, but the cost is
visible: the Non-Goals section was assembled from things already discovered
rather than used to hold the line while discovering them, and the scope grew
four times before anyone wrote it down.

## Book from what the write did, never from reading the state afterwards

Three separate defects had one cause: the call sites decided what to charge by
reading `nodeMapByKey` after the write. That cannot tell a write that installed
a node from one that lost LWW and left the incumbent alone, and it cannot tell
a first visit from a second.

Making `RHT.Set` report what it did — installed, revived, superseded — closed
all three, and made a token-type guard unnecessary: a node visited twice in one
traversal loses LWW on the second visit, so it charges nothing by construction
rather than by a special case.

The general form: when a mutation and its accounting are separated by a state
read, the read is where the bugs live. Return the delta from the mutation.

## A green suite is not evidence about a ledger

Every single defect in this work was invisible to the existing suite, both
before and after each change. The randomized differential — replay random
histories, compare the running size against one recomputed from content after
every step — found 333 divergences in 720 steps on code that was fully green.

And the differential was not enough on its own either. Its first version only
styled whole elements, so it never generated a range that opens inside one and
runs past its end, which is exactly the shape that drove Live negative. Review
caught that; widening the generator to straddle element boundaries was a
one-line change that turned a passing branch into a failing one.

Instruments have blind spots too. Ask what the generator *cannot* produce.

## Review caught a data-loss regression a green suite did not

Making `splitValue` shorten the left value in place — correct in itself, and
what Go has always done — exposed that `RGATreeSplitNode.deepcopy` **shared**
the value object where Go copies it. `Document.ensureClone` rebuilds the clone
from a populated root after a snapshot, an offline restore, or an update whose
callback threw, and every operation is applied to both, so the clone's split
truncated the root's node and the root then split an already-shortened value.
The tail after the split point was destroyed.

513 tests passed. No test ever did populated-root → clone rebuild → split.

The lesson is narrower than "write more tests": a change from *replacing* an
object to *mutating* it is only safe if nothing else holds a reference, and in
a CRDT with a clone-and-apply-twice model something always does.

## Cross-SDK parity is a property of net behaviour, not of matching code

Go's `RegisterGCPair` only adds to GC; the debit from Live is a separate
`AdjustDiffForGCPair` that the restore path deliberately does not call. The JS
equivalent folds both into one call. So identical-looking calls produce
opposite accounting, and the correct mirror of `GCOnlySize == nil` on one side
is `gcOnlySize` **set** on the other.

An agent doing the JS port contradicted an explicit instruction here and
attached a measurement showing that following it drove live to `{data:-4}`. It
was right. When porting between implementations, state the invariant in terms
of what the ledger should end up holding, not in terms of which arguments to
pass.

## Dead code plus a test named after it is worse than neither

The third accounting case ported from Go is unreachable in JS, because
`removeStyle` there skips removed nodes before the accounting runs.
Instrumented with a `throw`, it never fired once across 522 tests. The test
written for it passed for a reason unrelated to its name.

Removing it was right; so was recording at the call site *why* the server needs
the case and this SDK does not, with an explicit "do not close this gap from
this end". The reason is a behavioural divergence, and someone adding the
branch back to make the two files match would bury the real question.

## Try the rejected option before rejecting it

Storing every string raw was dismissed on reasoning, then implemented anyway to
see what broke. The existing suite answered in seconds: a caller's string `'1'`
read back as the number `1`. That is what turned a vague "narrow ambiguity" into
a precise boundary — only strings that are themselves JSON documents are
ambiguous — and the precise boundary is what made the fix shippable at fifteen
lines instead of unshippable.

The first instinct, "do not touch it", was too conservative by exactly one
experiment.

## A wire-format change has a release order, and it is not a code problem

Storing strings raw is simple code and a deployment hazard: a client without
the tolerant decode throws on a raw value, inside `applyChangePack` and before
the checkpoint advances, so it redelivers forever. That is the same
unrecoverable wedge the same PR removes for Go-authored values, pointed at
older clients of the same SDK.

Nothing in the diff shows this. It belongs at the top of the pull request, in
the release notes, and in the decision about which release each half lands in —
and it is the maintainer's call, not the implementer's.

## See Also

- `docs/tasks/archive/2026/09/20260921-attribute-ledger-and-representation-todo.md`
  — the plan and verification for this work
- `docs/tasks/archive/2026/09/20260920-post-gc-panic-and-snapshot-tombstone-lessons.md`
  — the preceding task, where the same ledger's tree half was fixed

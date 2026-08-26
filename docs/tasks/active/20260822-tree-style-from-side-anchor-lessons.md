# Lessons: from-side style range recovery

**Created**: 2026-08-22

## A skip filter and a recovery are mirror images, not variations

The end-side fix (§9.4, Fix 22) works because the applying replica
covers *too much*: filtering nodes out of an over-wide traversal is
safe. The from-side shape inverts the error direction — the traversal
covers *nothing* — so no predicate over visited tokens can help; the
traversal itself must be re-anchored. Recognizing which direction a
range-resolution bug errs in tells you immediately whether the fix is
a filter (subtractive) or a recovery (additive), and additive fixes
need a stricter trigger: here the recovery only activates when the
same merged-anchor shape holds for the from position *and* the
resolved range actually collapsed. Without the collapse check, a range
whose both anchors moved with the merge would be widened onto nodes
the styler never covered — probed explicitly by the ordered-range
control test before the trigger was final.

## A removal tombstone for a missing key is load-bearing

The obvious "simpler fix" — make `RemoveAttr` on a missing key a no-op
so neither replica materializes the container — breaks a different
concurrency: a concurrent `SetAttr` with an earlier ticket must lose
to the removal by LWW, which requires the removal to be recorded even
when the key does not exist yet. So convergence here means both
replicas carry the removal entry, not neither. This also invalidated
the issue's suggested oracle (`Attrs` empty on both): the converged
state is entry/entry, pinned as such in the regression test.

## Fixed-lamport unit contexts cannot express "unknown" by lamport

The unit `ChangeContext` issues all tickets under one lamport, so a
version-vector entry for the shared actor makes every local ticket
"known" and the §9.4 guard never activates. Simulating an unknown
concurrent merge in a unit test requires issuing the merge ticket
under a *different actor* absent from the styler's vector, not a
smaller lamport. The integration-level repro caught what the first
unit attempt could not.

## A per-replica shape check is not a convergence check

The first version of the regression test asserted, for each replica
independently, that the surviving `<p>` holds exactly one removed
`bold` entry. That is a shape assertion, and it passes even when the
two entries carry different identities or removal tickets — precisely
the internal divergence class the issue reports, since Marshal already
cannot see it. A convergence test has to compare the replicas against
*each other*: the entries are now rendered as sorted descriptors
carrying key, value, `updatedAt` and the removal flag, and the two
lists are compared directly. The per-replica assertions stay, because
"identical on both replicas" alone would also be satisfied by both
being empty.

The same review pass replaced the remaining `assert` calls guarding a
dereference with `require`. `assert` does not stop the test, so a
missing survivor or attribute container panicked on the next line
instead of reporting the failure that had just been detected.

## PBT confirms a fix by moving, not by passing

As in every previous cycle, the pinned-seed PBT run does not go green
after the fix — the shrinker slides to the nearest surviving
counterexample (here: a to-side variant where the range end's left
sibling is the moved merge-source tombstone, plus the known edit-only
divergence). The verification signal is that the *reported* shape no
longer minimizes, and that the new counterexamples reproduce on main
before the change (checked with `git stash` on the JS side). Treating
"PBT still fails" as fix failure would deadlock the loop; treating it
as success without the stash check would hide regressions.

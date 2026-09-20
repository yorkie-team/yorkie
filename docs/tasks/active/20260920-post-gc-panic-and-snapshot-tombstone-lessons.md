# Lessons — post-GC style panic and the snapshot's lost text attribute tombstone

**Created**: 2026-09-20

## Grepping for `recover()` does not tell you whether a panic kills the process

The starting premise was: `recover()` appears only in test files, therefore a
panic reachable from an RPC handler takes the process down, therefore #2008 is a
P0. The grep was correct and the conclusion was wrong. The RPC server is a plain
`http.Server` under `h2c` (`server/rpc/server.go:102-108`), and both net/http and
x/net/http2 recover handler panics — per connection and per stream respectively.
Application code containing no `recover()` says nothing about survivability until
you have looked at the transport underneath it.

The corollary is the part worth keeping: the goroutines that matter are the ones
that do **not** run under a transport handler. Here that was `be.Go` in
`server/backend/background/background.go:85`, which has no recover of its own.
Ask "which goroutines are outside the handler stack", not "does the repo call
recover".

## "Drop any step and the bug disappears" means conjunction, not a single cause

Issue #2008's table showed that removing any one of five steps made the panic
vanish. That reads like a single fragile cause, and it is the opposite: it is the
signature of **two defects that must both fire**. The split-and-remove hole breaks
the undo stack's LIFO ordering; the restore hole then builds an orphaned live node
under a tombstone. Each is harmless alone.

The move that settled it was constructing the *same corrupt state without the
first defect* — no split anywhere, a correctly aligned undo, and a peer removing
the enclosing span. Identical orphan. That single experiment is what proved the
two are independent, and it is what stops the follow-up from being wrongly closed
against the existing split-undo task, whose fix would not touch the real defect.

When a minimal reproduction is this brittle, try to reach the same end state by a
different route before believing you have found one cause.

## A regression test anchored on an unfixed second defect goes vacuously green

The first version of the #2008 test only failed if the recovered value was a
`runtime.Error`. It passed when nothing panicked at all. Since the state it
depends on exists only because of the *unfixed* 1b defect, fixing 1b would make
`Style` succeed, `recovered` would be nil, and the test would go green while
guarding nothing — silently, with no one to notice.

Tightening it to `require.ErrorIs(err, crdt.ErrNodeNotFound)` makes it
self-invalidating: the day 1b lands, it fails loudly and somebody decides whether
to retire or re-anchor it. A test whose premise can be removed by someone else's
fix should assert the premise, not just the absence of a crash.

## Verify a fix by reverting only the production files

`git checkout <fix>~1 -- <production paths>`, keeping the test, then re-running,
is what separates a real test from a decorative one — and it catches the case
where a fix is split across files and only one half is load-bearing. For #2006
both files were reverted independently and the test failed either way, which is
how we know neither line was decoration.

Committing the test and the fix **together in one commit** is what makes this
possible. Splitting them across two commits would have meant a commit with a
failing test, and the repo requires every commit to be green.

## Incidental protection is worth writing down in the commit

The server survives #2008 only because `BuildInternalDocForServerSeq` hands out a
`DeepCopy`, and `NewTree` re-registers only nodes reachable from the index root.
Those copies exist for cache-aliasing reasons. Nothing in the code says a safety
property depends on them, and removing one looks like a free allocation win.

A latent crash held off by an unrelated implementation detail should be stated at
the place someone would change that detail — here, the commit body — or the
protection disappears with the next optimization.

## A guard at the crash site is not a fix, and the issue should say so

After this change the reproduction still panics: `json.(*Tree).Style` does
`panic(err)` by convention, so only the *fault* was eliminated, not the panic.
And the document is still inconsistent — the orphaned live node is untouched.

Both facts belong in the issue when it is closed. A fix that changes the symptom
class is worth shipping, but calling it resolution hides the defect it was
protecting against.

## A candidate that breaks no test can still be the worst option available

The GC-site candidate for 1b broke zero existing tests, added one passing test,
and read as a clean strictly-positive fix. It made two replicas render different
content based on when each happened to run garbage collection — a purely local
decision no replica coordinates with any other.

It was invisible to the suite for a structural reason worth naming: **no test in
the repository compared two replicas that received the same changes in a
different order.** A whole class of defect had no instrument. Adding one turned
a question that looked like it needed a senior judgement call into a
measurement, and the measurement disagreed with the reasoning — the "obvious"
one-line fix was insufficient and the plausible second option was actively
harmful.

When a change touches convergence, build the instrument before evaluating
candidates. A green suite is evidence about the tests you have, not about the
property you care about.

## Compare a restored node against its siblings, not against itself

Both ticket candidates were first measured the obvious way: does the restored
node carry the same ticket across all delivery orders? That question was nearly
decisive but not quite — it compares the node only against itself.

The sharper question was whether it agrees with **its own never-purged
siblings** — the pieces split from the same insertion that the same removal
swept. `parent.RemovedAt()` agrees 6 of 6; the alternative breaks ranks 3 of 6.
A restored node is a re-materialisation of something that already existed and
was already swept, so the gold standard is "indistinguishable from what it would
have been had it never been purged". That framing picked the winner in one
measurement where the self-comparison left room to argue.

## A boolean cannot detect a ticket divergence

The harness's state dump recorded `removed` as a bool. Under the rejected ticket
variant two replicas held `3:1:AB` and `4:1:AC` on the same node while every
field of that dump matched — a false green. Only a direct ticket comparison saw
it.

If a value participates in LWW, assert the value. Asserting the predicate
derived from it hides exactly the disagreements that matter.

## Write regression tests so they fail when their premise is removed

`TestTreeStyleAfterCollect` depended on a state that a *different, unfixed*
defect produced. Tightened to require the guard's specific error, it failed the
moment that defect was fixed — loudly, in the same session, with an obvious
cause. Left as "assert nothing crashed", it would have gone green while
guarding nothing and nobody would have known.

The fix was to split it: the end-to-end sequence stays as the issue's
reproduction and now asserts reachability, and the guard moved down to the layer
it actually lives at, where it can be driven directly and does not depend on any
higher-level sequence still being able to reach it.

## See Also

- `docs/tasks/active/20260920-post-gc-panic-and-snapshot-tombstone-todo.md` — the
  plan and the review for this work
- `docs/tasks/active/20260816-tree-split-edit-loses-undo-entry-todo.md` — the
  split-undo limitation this deliberately does *not* get folded into

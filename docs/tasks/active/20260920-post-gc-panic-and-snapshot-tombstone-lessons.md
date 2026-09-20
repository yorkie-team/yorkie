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

## See Also

- `docs/tasks/active/20260920-post-gc-panic-and-snapshot-tombstone-todo.md` — the
  plan and the review for this work
- `docs/tasks/active/20260816-tree-split-edit-loses-undo-entry-todo.md` — the
  split-undo limitation this deliberately does *not* get folded into

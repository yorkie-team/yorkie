# Lessons: Ticket.Key caches its key without synchronization

**Created**: 2026-10-25

## A lazy cache turns a read into a write

`Key()` reads like an accessor at every call site, and `*Ticket` is treated as
an immutable value — shared by pointer, used as a map key, exported as two
package-level singletons. The lazy cache quietly made the first call a write
to shared memory, so sharing the value became unsafe without any call site
looking like it mutated anything.

The tempting fix is to fill every derived field in the constructor. Here that
was wrong: see the next lesson.

## Fix the shared instances, not the type

The race needs two goroutines on one instance, and only three instances are
ever shared across goroutines — the package-level singletons. Making every
ticket eager to protect those three cost +21–149% allocs/op on the decode and
apply benchmarks, because most tickets there never have their key read.
Filling the singletons' caches in `init` closes the race at zero per-instance cost.

Before changing a hot constructor, ask which instances actually exhibit the
bug, and measure allocs/op against `main` — CI's `bench` job has no baseline,
so a green run says nothing about a regression.

## The same shape usually appears more than once in a file

The issue named `Key()`. `ActorIDBase64()` ten lines below had the identical
lazy write to `cachedActorIDBase64`, racing on the same shared tickets. Fixing
only the reported method would have left the detector firing on the next run
and the flake unexplained.

## `assert` on a constructor hides the failure behind a panic

The integration lane went red on an unrelated transient — `server.New` in
`clusternodes_test.go` could not reach MongoDB — but the test checked it with
`assert.NoError` and then called `Start()` on the nil server it got back. The
nil dereference panicked the whole test binary, so a single flaky subtest took
down every other package result and buried the one line that said what had
actually gone wrong.

Any check whose value is dereferenced afterwards belongs in `require`, not
`assert`: `assert` is for the facts under test, `require` for the setup that
the rest of the body cannot run without.

## Test fixtures should not run more of the system than they assert on

The same file cleared cluster nodes by booting a throwaway server per subtest —
an RPC port, a housekeeping loop, a membership loop that re-registers the very
row just cleared, and one more MongoDB connection — to call a single
`RemoveClusterNodes`. Dialing the database directly does the same work with one
connection for the whole test, and removes the race between the clear and the
throwaway server's own re-registration.

## A race that "writes the same value" still fails the build

Every goroutine computes an identical string here, so the race is close to
benign in practice. That is not a defence: the Go memory model gives no
guarantee for a concurrent string-header write, and `-race` fails CI
regardless. A flake nobody can reproduce in 1500 repeats is exactly what an
almost-benign race looks like from the outside.

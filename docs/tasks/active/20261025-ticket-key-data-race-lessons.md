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
Warming the singletons at init closes the race at zero per-instance cost.

Before changing a hot constructor, ask which instances actually exhibit the
bug, and measure allocs/op against `main` — CI's `bench` job has no baseline,
so a green run says nothing about a regression.

## The same shape usually appears more than once in a file

The issue named `Key()`. `ActorIDBase64()` ten lines below had the identical
lazy write to `cachedActorIDBase64`, racing on the same shared tickets. Fixing
only the reported method would have left the detector firing on the next run
and the flake unexplained.

## A race that "writes the same value" still fails the build

Every goroutine computes an identical string here, so the race is close to
benign in practice. That is not a defence: the Go memory model gives no
guarantee for a concurrent string-header write, and `-race` fails CI
regardless. A flake nobody can reproduce in 1500 repeats is exactly what an
almost-benign race looks like from the outside.

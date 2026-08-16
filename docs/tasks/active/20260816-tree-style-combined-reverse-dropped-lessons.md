# Lessons: Tree Style combined reverse dropped on execute

**Created**: 2026-08-16

## "Reachability" is not just "concurrency vs. not" — check the failure shape too

The first instinct was to file this alongside the other entries in
`20260816-remote-redo-replica-divergence-todo.md`, since it was found during
the same task chain and by the same "read the JS source in full" discipline.
But that document's own name describes its failure shape: divergence — some
replicas end up disagreeing with others, usually because a guard or
deregister step only fires under one `OpSource`. This defect has a different
shape: every replica that executes a combined reverse drops the removal
identically. That is uniformly wrong, not divergent — recoverable by a later
coordinated fix, not a permanent split. Filing convergent-but-wrong bugs and
divergent bugs in the same bucket makes the collecting document harder to
triage later ("is this one urgent because it splits state, or is it merely
wrong everywhere?"). The tell was in my own draft: the reachability
paragraph I wrote said "does not need concurrency the way several other
entries in this document do" — that sentence was itself the signal to split
the file, and I read past it the first time.

## The reviewer supplied the argument I only asserted

I had verified the JS if/else shape and its provenance (git log, PR
numbers), and I made the call to port it as-is per this project's stated
rule. But I framed my concern as "this undercuts the feature we just built"
— a UX/completeness argument. The decisive argument for *why the rule must
hold here specifically* was different and stronger: Go is the server, not
just a peer SDK. `change.Execute` replays every operation server-side, so a
unilateral Go fix doesn't just create "a nicer Go behavior than JS" — it
creates permanent server-vs-client divergence for every JS client's own
undo. I had the right instinct (escalate, don't guess) but not the sharpest
version of the argument for why. Escalating with "I think X, can you check"
is still worth doing even when the requester ends up supplying the reasoning
you didn't have — the alternative (silently picking a side) would have been
worse regardless of which side turned out right.

## A converter fix changes the meaning of already-persisted bytes, not just future writes

Fixing `fromTreeStyle`'s exclusive decode (the Critical-shaped bug) was
correct and necessary. But it has a side effect worth writing down
explicitly, not just fixing and moving on: any combined `TreeStyle` reverse
that was persisted *before* the fix was decoded as removal-only at write
time; after the fix, decoding the same historical bytes now recovers both
fields and executes differently. The new behavior is the right one — this
is the same trade the Text `fromStyle` fix already made — but "the fix
changed how we read old data" is exactly the kind of fact that should be a
one-line note in the task file, not something a future compaction or
snapshot-rebuild investigator has to rediscover from first principles.

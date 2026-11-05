**Created**: 2026-11-05

# Lessons — born-tombstoned tree node purged too early

## The tombstone ticket is a GC schedule, not a label

`removedAt` reads like "when was this deleted", but the only thing that
consumes it at scale is `minVersionVector.EqualToOrAfter(removedAt)`. So
picking a ticket is picking a date, and the right question is never "which
operation removed it" but "after which event is nobody still pointing at
it". For a node born under a concurrently removed parent that event is the
PARENT's removal reaching every replica — the inserter is pointing at the
node precisely because it has not seen that removal yet.

## Divergent purge schedules hide behind identical XML

Both replicas rendered the same document the whole time: the node was
invisible on B (tombstoned) and invisible on A (live, under a removed
parent). Every convergence assertion that compares rendered content passes.
The bug only surfaces one change later, as an apply failure — so a test that
syncs and compares XML would not have caught it. The test has to GC with a
realistic (lagging) minimum vector and then deliver another change.

## The fix was already written down in the same file

`recreateFromSpan` carries a long comment explaining why it stamps the
parent's `removedAt` and not `executedAt`, including "removedAt feeds
canDelete, so they would then collect on different passes" — the exact
failure, predicted. Phase 8 just never got the same treatment. When a file
already argues a rule in prose, grep for the other sites that should obey it
before reaching for a new mechanism.

## Test-helper semantics decided the test

The first attempt used `exchangeInOrder` with an empty arrival list for the
lagging replica, which looks like "A receives nothing". It is not: the helper
self-acks every replica's pack regardless of delivery, so B's removal was
consumed and could never be delivered later. The symptom was a third subtest
that purged nothing for no visible reason. Reading the helper's ack loop,
rather than trusting its arrival-order argument, was the fix.

## Self review

Not run. The autonomous run that produced this branch has no tool able to
dispatch the reviewer subagent `/self-review` requires, and its rules forbid
substituting a self-read of the diff. This is a skipped round, not a clean
one — the PR is opened as a draft and still needs CI, `@claude review`, and a
human.

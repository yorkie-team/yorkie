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

## Integration lane failure on this branch is not this branch's

The `test` lane failed once on
`TestClusterNodes/Concurrent connection leadership election test`
(`test/integration/clusternodes_test.go`), which starts five servers and
asserts within 1s that exactly one of them reports `IsLeader()`. With
`LeaseDuration` 300ms and `RenewalInterval` 100ms, a renewal that loses its
slot under the race detector on a loaded runner demotes the leader, and the
window to elect a successor and observe `freq == 1` is three renewal periods
wide. The same family already needed #1820 ("Fix flaky leadership revocation
and LRU hit-rate tests"), and `main` is green over the last twelve runs.

Nothing on this branch reaches leadership: the change is confined to the
`fromParent.IsRemoved()` branch of `crdt.Tree.Edit`, and its test is an
in-memory `document_test` with no server, no MongoDB and no build tag. The
timing budget belongs to a file outside this PR's diff, so it is left for a
separate change.

## Self review

Not run. The autonomous run that produced this branch has no tool able to
dispatch the reviewer subagent `/self-review` requires, and its rules forbid
substituting a self-read of the diff. This is a skipped round, not a clean
one — the PR is opened as a draft and still needs CI, `@claude review`, and a
human.

## The stamp is also what the two tombstone-ticket readers want

Two readers outside the diff ask whether an operation's author already knew a
node was gone: `tombstoneKnown` in the delete walk
(`pkg/document/crdt/tree.go:3223`, feeding `canDelete` at 644) and `seenGone`
in `appendUnknownSplitSiblings` (3302). Both are reached only when the
operation's vector already knows the node's `createdAt`.

Under the old stamp, a born-tombstoned node carried the insert's ticket, which
*is* its `createdAt` — so in that branch both readers answered "known gone"
unconditionally, for every author, including one who had received the insert
but not the parent removal and therefore saw the node live. That is the wrong
answer to the question each reader documents itself as asking: 3223 says
"existing tombstone was already causally known", and the walk's contract
(3269-3272) says it passes a sibling only if the editor "saw it gone: removed
by a change the editor knew". The change that removed a born tombstone is the
parent's removal, not the insert.

With the parent's ticket both readers answer `knows(T_rem)`, which is exactly
"did this author see the removal". The consequences are benign in both
directions:

- `canDelete` can now return true where it used to return false, so a
  concurrent delete LWW-overwrites the stamp. `remove()` overwrites only on
  `removedAt.After(n.removedAt)` (627), so a stamp can only move forward, and
  coverage of the new ticket implies every replica — the inserter included —
  applied the delete that wrote it. The purge can be delayed, never advanced,
  which is the property this task is about.
- `seenGone` can now be false, stopping the split-sibling walk at a node the
  editor saw live. That is the documented rule, not a regression from it.

`TestTreeBornTombstoneConvergesWithAThirdPartyDelete` pins the third-replica
case end to end: a delete from a replica that knew the insert but not the
removal converges on all three replicas and leaves nothing stranded in GC.

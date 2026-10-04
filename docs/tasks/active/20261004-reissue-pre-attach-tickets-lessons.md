**Created**: 2026-10-04

# Lessons — Re-issue pre-attach tickets on attach

## Notes

- `SetActor` was not just missing the root: it also left the version
  vector keyed by the initial actor (`ID.SetActor` had its own TODO), so
  every pre-attach change pushed a `000…` entry into the server's vector.
  The re-issue re-keys it.
- The initial actor is also the server's actor. Admin `UpdateDocument`,
  revision restore and compaction write elements under it, and
  `BuildDocForCheckpoint` calls `SetActor` on such a document. Making
  `SetActor` itself re-issue would have rewritten server-written elements;
  the re-issue lives in a separate method that only `Client.Attach` calls,
  behind a "never synced" guard.
- The ticket that must never move is `time.InitialTicket` (lamport 0): the
  root object and sentinel nodes share it across replicas. Every ticket a
  change mints carries the change's lamport (≥ 1), so "lamport > 0" is the
  whole exclusion rule.
- Going through the wire format made the rewrite small: a generic
  `protoreflect` walk over `TimeTicket` covers every operation field. The
  one trap is that Object/Array/Tree values travel as encoded
  `api.JSONElement` bytes, which the walk has to decode and re-encode.
- Rebuilding the root by replay, instead of rewriting it, made "local
  root equals server root" true by construction, and the unit test checks
  it byte for byte against a server-style rebuild.
- On `main` the integration test shows both failure shapes in one run:
  the two replicas of a round diverge, and a later round ends at `{}`.

## Self-review

### Round 1 — correctness/tests

- Blocking, fixed: the wire drops a Text value's content, so a pre-attach
  Undo of a removed Text came back empty after the round trip -- silently
  (`{"t":"hello"}` -> `{"t":""}`), or, with a later Edit on its nodes,
  as a replay error that failed every attach retry. Text values of
  Set/Add/ArraySet are now re-issued through the snapshot encoding.
  Regression test: "a Text restored by undo keeps its content" (red
  without the fix).
- Added coverage for tree split + undo/redo, TreeStyle, ArraySet and a
  counter; DocSize and GarbageLen are unchanged by the re-issue.
- Not changed: the ticket check in the test reuses the walker's shape, so
  it cannot find a ticket the walker cannot reach. The reviewer found no
  missed field at the proto level; the model-level checks (Marshal,
  server rebuild byte equality) back it.
- Not changed, documented: undo changes keep locally skipped ops, and
  pre-attach history is cleared on attach.
- The compaction log change was dropped at rebase: #2108 made the same
  change on `main`.

## Bench

- `BenchmarkRPC/attach_large_document` passes at the default benchtime
  (what `make bench` in CI runs: 2-3 iterations, 3/3 green). With
  `-benchtime 10x` it fails most runs: from the third iteration an attach
  response exceeds the 50 MB message limit. The bench reuses one key, and
  each iteration now leaves a correct LWW loser -- a 10 MB Text tombstone
  -- that GC cannot purge until every client's version vector passes it.
  The legacy path "passed" because the colliding createdAt dropped
  elements. Giving each iteration its own key would fix the bench; left
  as is per the task, flagged in the PR.

## Review panel: security round

- Upheld: the server read the DocChanged publisher straight from
  `pushedChanges[0].ActorID`, and nothing upstream proves a pushed
  change's actor belongs to the authenticated client. Since the pubsub
  self-echo filter drops events whose `Actor` equals the subscriber, a
  client stamping a victim's actor into a change suppressed the
  victim's `DocChanged`. Making the client rewrite actors wholesale
  (`reissue`) did not create the hole, but it is the same trust
  boundary, so it was closed here.
- Fix: `publisherActor` in `server/packs/pushpull.go` honors the stamped
  actor only when `clientInfo.IsOwnActor` accepts it, and otherwise
  falls back to the session id the client provably owns. Mismatches are
  logged. Worst case is now a spurious self-echo for the sender, never a
  dropped event for another client.
- Not changed: pushes still accept a foreign actor into the changes
  collection. Rejecting outright would break clients that legitimately
  push pre-attach changes stamped with `InitialActorID` -- the very case
  this task exists for -- and it is wider than the reported finding.

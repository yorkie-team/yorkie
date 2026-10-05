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
  the re-issue is opt-in (`SetActorWithOptions` with `WithReissue()`),
  which only `Client.Attach` passes, behind a "never synced" guard.
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

### Round 2 — correctness/tests

- Blocking, fixed: uniqueness held between clients but not within one.
  The re-issue keeps each ticket's lamport, and a fresh `Document`
  starts them at 1, so a second never-synced document of a key the same
  actor already attached minted the very `createdAt` the first attach
  pushed. `Client.claimReissue` records the actor per key and declines
  the re-issue for the repeat, which falls back to `SetActor`. What it
  cannot cover is a new process that reactivates with an explicit client
  key and so takes the same actor; that needs the server's lamport for
  the key, which the client does not have before the round trip.
- Blocking, fixed: the per-change version vectors are re-keyed too, and
  nothing asserted it -- `localActorsOf` reads only the change actor, and
  the walk cannot see a change ID. `assertLocalVectors` now checks every
  pushed change's vector names the new actor, at that change's own
  lamport, and no one else. Red with the re-key removed.
- Blocking, fixed: the test for the declined re-issue read both elements
  back through the second document itself and dereferenced a nil Text in
  CI. `pullChangeInfos` drops a pulled change whose actor is the pulling
  client's and whose clientSeq the client's checkpoint already covers, and
  a second `document.New(key)` restarts its clientSeq at 1 -- the one the
  first attach pushed under -- so the second document never pulls the
  first's change. It is the server's own-echo filter, not the re-issue:
  the same hole is there on `main`, where both documents push under the
  same actor too. The test now reads the merged state through another
  client, which is where "the server can tell the two elements apart" is
  observable. Closing the hole itself needs `server/packs`, outside this
  change.

## Bench

- `BenchmarkRPC/attach_large_document` passes at the default benchtime
  (what `make bench` in CI runs). With
  `-benchtime 10x` it fails most runs: from the third iteration an attach
  response exceeds the 50 MB message limit. The bench reuses one key, and
  each iteration now leaves a correct LWW loser -- a 10 MB Text tombstone
  -- that GC cannot purge until every client's version vector passes it.
  The legacy path "passed" because the colliding createdAt dropped
  elements. Giving each iteration its own key would fix the bench; left
  as is, flagged in the PR.

## Scope

Mechanisms tried during review and taken back out, and why:

- An attach rollback that restored the previous actor on failure. It
  cannot be made safe when the `AttachDocument` response is lost: the
  client cannot tell whether the server stored the re-issued pack, and
  restoring the initial actor re-creates the colliding `createdAt`. A
  re-issued document is a valid detached document, and the rollback only
  preserved undo history that a successful attach clears anyway.
- A push-side check that a change's actor belongs to the pushing client.
  It relies on `client_id` and `StableActorID`, which are not
  credentials, so it narrows the gap without closing it. Tracked in
  #2114 with the client identity model.
- HLL registers on `JSONElementSimple`, a version-vector size cap, an
  error-code change and undo-change pruning. Each is independent of the
  re-issue; the re-issue does not need any of them.

The re-issue is exposed as an option of `SetActor` rather than a second
verb: `SetActorWithOptions(actor, WithReissue())`. Without the option it
is `SetActor`, so the attachable interface and every existing caller
stay as they are.

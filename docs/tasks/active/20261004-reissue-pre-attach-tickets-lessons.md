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

## Loop round 4 follow-up (manual)

- The panel upheld that the pull dedup (`pullChangeInfos`) trusts a
  client-supplied change actor. Fixed at the door instead of per consumer:
  `PushPull` now refuses (`ErrInvalidChangeActor`) any not-yet-acknowledged
  change whose actor the client does not own. `publisherActor` lost its
  mismatch branch, since a pushed change's actor is now owned by
  construction.
- Legacy check before enforcing: old Go and JS clients already stamped the
  change ID with the client's actor (`SetActor` rewrote it on attach); only
  the tickets inside kept the initial actor. The server's own pushes use
  `SystemClientInfo` (ID = initial actor), the cluster presence-clear uses
  the session id, and compaction bypasses `PushPull`. So no legitimate
  pusher sends a foreign or initial actor, and no exception was needed.
- Not fixed, documented: `client_id` is not bound to a credential
  (project-scoped auth). That is the server's identity model, not this PR.
- Rollback fixes from the same round: the fallback rollback is now guarded
  by `neverSynced` (it could revert the actor of a document whose attach
  already applied), the undo/redo stacks are restored only after a real
  re-issue, and `DeepCopy` keeps `absorbedRemote`.

## Loop round 6 follow-up (panel)

- Upheld: the forward re-issue had the same blind spot the round-5
  rollback guard closed. After a pushed-but-failed attach, nothing local
  records the push, so attaching the document again -- same client after a
  reactivation, or another one -- re-issued elements the server already
  held, under a second actor.
- Fix: the push is now recorded on the document itself
  (`InternalDocument.MarkPushed`, `Document.MarkPushed`, carried by
  `DeepCopy`), and `neverSynced` reads it. One flag covers both
  directions: the rollback declines, and a later `ReissueActor` falls back
  to `SetActor`, which moves the change IDs and leaves every stored ticket
  alone. `Client.Attach` sets it from the `pushed` report and then calls
  the rollback unconditionally, since the rollback now decides for itself.
- Reproducer: `TestReissueActor/a pushed but failed attach is never
  re-issued again` -- without the guard the retried attach renames the
  stored elements and diverges from the server's replica.
- Disputed, not changed: the panel re-raised `client_id` not being bound
  to a credential as a blocking finding on `server/rpc/yorkie_server.go`.
  That is the project-scoped identity model this branch does not touch;
  rebuttal filed. The one claim this PR did own -- the design doc stating
  the residual bound as "learns another client's session id" -- was wrong,
  since `IsOwnActor` also accepts the `StableActorID`, which
  `types.DeriveActorID` derives from the project id and the client key.
  The doc now names both ways in.

## Loop round 7 follow-up (panel)

- Upheld in part: the `validateChangeActors` comment claimed the guard
  backs "VV and GC bookkeeping", but the pack's `VersionVector` -- the
  sibling client-supplied identity input -- is stored verbatim by
  `UpdateMinVersionVector` and never checked. No validation was added:
  ownership is the wrong predicate for a version vector, which
  legitimately carries other actors' lamports. The comment now states the
  gap and its bound -- the vector lands in the pusher's own
  `VersionVectorInfo` row and `MinVersionVector` scores an actor missing
  from any row as `0` (`pkg/document/time/version_vector.go:62`), so a
  forged entry can only stall tombstone GC, never drop tombstones early.
- Disputed again, not changed: `client_id` resolving to an unauthenticated
  principal. Same project-scoped identity model as round 6; rebuttal
  re-filed. What did change is the framing the finding objected to: the
  gate no longer reads as an authorization control. `FindActiveClientInfo`
  now says on its face that it returns a self-asserted identity, and the
  design doc carries a "Security boundary" section splitting what the gate
  enforces, what it does not, and what it does not cover.

## Loop round 8 follow-up (panel)

- Upheld: the Watch comment claimed "a client cannot subscribe under
  another client's presence identity". It can. `StableActorID` is
  `types.DeriveActorID(projectID, clientKey)` (`api/types/actor.go:48`)
  and `ActivateClient` inserts a fresh row for a key already in use
  (`server/backend/database/mongo/client.go:1096`), so activating under
  the victim's client key yields a client that genuinely owns the
  victim's stable actor. The collision is the point of the derivation --
  one logical client resumes one actor across sessions -- so the fix is
  in the claim, not the derivation: the Watch comment, the
  `validateChangeActors` scope note, `FindActiveClientInfo` and the
  design doc now all name the client key as an identifier, not a secret,
  and list key-collision beside `client_id` presentation as the two ways
  in.
- Upheld and fixed in code: the "blast radius is bounded" argument for
  the unchecked `ChangePack.VersionVector` only covered value direction.
  Entry count was bounded by nothing but the 16 MiB pack, and every
  entry is persisted per client row, cached per document and unioned
  into the minVV sent to every other client. `validateVersionVectorSize`
  now caps the count (`maxVersionVectorEntries`); membership stays
  unchecked, because a version vector legitimately carries other actors'
  lamports. The cap sits far above any real document: nothing prunes
  detached actors today, so a legitimate vector grows with lifetime
  writers.
- Also corrected: the direction argument quietly assumed every client
  has a `VersionVectorInfo` row to clamp against. `DisableGC` clients
  have none (`updateVersionVector` skips them), which the GC opt-out
  already intends -- but it means the clamp covers GC-tracked clients
  only, and both the comment and the design doc now say so.
- Disputed a third time, not changed: `client_id` resolving to an
  unauthenticated principal. Per-client credentials are a protocol
  change this branch does not carry; rebuttal re-filed so the standstill
  is on the record.

## Loop round 9 follow-up (panel)

- Upheld in part, and fixed in code: the Watch actor check still raised
  `ErrActorMismatch` as `PermissionDenied`, so the one remaining place
  the branch advertised an authorization boundary was the status code
  itself -- every prose claim had already been bounded in round 8. The
  check compares two self-asserted fields of the same request
  (`actor_id` against the `StableActorID` of `client_id`), which is a
  malformed request, not a denied one, and `PermissionDenied` also
  reaches the SDKs' auth-error path (`watch-access-revalidation.md`).
  `ErrActorMismatch` is now `InvalidArgument`, matching its sibling
  `ErrInvalidChangeActor` in `validateChangeActors`. The `WithCode`
  string is unchanged, so `converter.ErrorCodeOf` consumers and the
  RPC testcase keep matching.
- Disputed a fourth time, not changed: the underlying bypass -- the
  project API key admitting a caller that can present any `client_id`
  or activate a duplicate row under a victim's client key. Per-client
  credentials are a protocol-level change this branch does not carry,
  and `ActivateClient`'s duplicate rows are load-bearing for actor
  resumption across sessions on a sharded `ColClients` with no
  enforceable `{project_id, key}` uniqueness. Rebuttal re-filed; this
  is the fourth round, so a human decision on the identity model is
  what the standstill actually needs.

## Loop round 10 follow-up (panel)

- Upheld, and the root cause fixed rather than the symptom: repairing a
  dedup `Counter`'s HLL registers inside `reissue.go` was local-only.
  The operation the attach then pushes is encoded by the same
  `toJSONElementSimple`, which had no field for the registers, so the
  re-issued replica and the server would have disagreed about the
  counter forever -- a divergence worse than the symmetric loss it
  replaced. `JSONElementSimple` now carries `hll_registers = 6`,
  `toJSONElementSimple` fills it and `fromCounterElement` restores it,
  so the push path and the snapshot encoding are finally in step. The
  `reissue.go` special case is gone; the round-trip is lossless at the
  layer below it.
- Same field, same fix, different caller: `CompactDocument` stores
  `newDoc.CreateChangePack().Changes`, whose `Set` values go through
  `toJSONElementSimple` too. A document seeded from YSON -- which is
  the only way a `Set` ever carries a non-empty sketch -- had its
  dedup counters silently emptied by compaction. Pre-existing, not
  introduced by this branch, and `compaction.go` needed no change of
  its own once the encoding carried the registers.
- The field is additive and backward compatible: a peer that predates
  it sends nothing and decodes exactly as it does today. The JS SDK
  needs the mirror change before a JS client can seed a dedup counter
  from YSON; until then the loss there is unchanged, not worsened.
- Upheld: `ReissueActor`'s `prev == actor` early-out skipped the
  `mintedActors` sweep. A caller that renames a document through the
  public `Document.SetActor` to the very actor it then attaches under
  left a root full of `InitialActorID` tickets to be pushed. The guard
  is now `prev != actor || len(d.mintedActors) > 0`; the new subtest
  "a document renamed to the attaching actor still re-issues" fails
  without it (84 initial-actor tickets survive).

## Loop round 11 follow-up (panel)

- Re-filed, with provenance this time rather than restating the
  limitation. The panel raised the same two findings a fourth time:
  `validateChangeActors` is bypassable by activating under a victim's
  client key, and the Watch `actor_id` check inherits the bypass. Both
  are accurate about the mechanism and neither is reachable from this
  branch's diff.
- Every component of the residual bypass predates the branch and is
  untouched by it. `git diff origin/main...HEAD -- api/types/
  server/backend/database/` is empty, so `types.DeriveActorID`
  (`api/types/actor.go:48`) and `ActivateClient`'s unconditional
  `InsertOne` (`server/backend/database/mongo/client.go:1098`, `:1108`)
  are main's, from #1969. The Watch check the second finding names is
  on main verbatim at `origin/main:server/rpc/yorkie_server.go:655`;
  this branch edited only the comment above it. The pull dedup the
  first finding's divergence chain runs through is main's too, at
  `origin/main:server/packs/pushpull.go:636`.
- What the branch adds to `pushpull.go` is the guard itself, which
  strictly narrows the push path: before it, a pushed change could be
  stamped with any actor at all and no server check looked. The finding
  measures the guard against an authorization boundary Yorkie does not
  have anywhere in its document API, rather than against the state it
  replaced.
- Closing it for real still needs per-client credentials at the
  protocol level. The duplicate-row behaviour is load-bearing for actor
  resumption across sessions, and `{project_id, key}` uniqueness is not
  enforceable on a sharded `ColClients` (offline-resumable-attach.md,
  "Alternatives"). Four rounds in, the standstill needs a maintainer
  decision on the identity model, not another fixer round.

## Re-scope (after loop round 13)

- Each loop round answered the newest finding with a new mechanism, and
  each mechanism drew new findings: the rollback needed a pushed-mark,
  the pushed-mark needed minted-actor tracking, the server-side actor
  check needed StableActorID to be unforgeable. Thirteen rounds later
  the PR touched the wire protocol. The fix was to ask what the original
  intent needed, not what the newest finding wanted.
- Dropping the rollback removed a whole class of findings at once. A
  re-issued document is a valid detached document; the rollback only
  preserved undo history that a successful attach clears anyway, and on
  a lost response it could not be made safe.
- The server-side actor binding is real but belongs to the client
  identity model (`client_id` and `StableActorID` are not credentials):
  #2114, cross-linked with #2113.

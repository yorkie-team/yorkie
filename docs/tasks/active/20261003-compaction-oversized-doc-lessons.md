# Lessons: compacting a document past MongoDB's record limit

- **A second test server is not a cold read.** Servers sharing the test
  database join one cluster and forward document RPCs to the owner, which
  answers from its caches. Attaching through a fresh `helper.TestServer()`
  still returned the purged content. The durable check is the change count
  read back through `be.DB` after the purge has dropped the change cache.
- **A bench that hangs past `go test -timeout` is outside the binary.** Under
  `-bench` neither the binary's alarm nor `cmd/go`'s kill timer runs, but the
  hang here was the runner choking on one log line, found only in the live
  job log; cancelled jobs keep no log.
- **A fix can surface a limit the bug was hiding.** The bench passed on main
  because the document it compacted had already lost its content.

## Self review

- Round 1 (correctness, tests; general-purpose reviewer agent, not the CI
  panel): no blocking findings, loop ended. Applied: `bson.D` with `_id`
  first for deterministic bytes, a comment on the cache in the test's second
  read. Deferred to the PR body: purge and insert are still not atomic for
  other failures (network, stepdown, the `server_seq` conflict check after
  the purge); an oversized document is rebuilt and rejected every
  housekeeping cycle.
- Round 2 (`/code-review high` on #2108, after the PR opened; 8 findings,
  none blocking). Fixed: `server_seq` checked before the purge, as the memory
  DB already does (new `RunCompactChangeInfosTest`, Red on mongo: 3 changes
  became 1); the integration test counts changes straight from MongoDB
  instead of through `FindChangeInfosBetweenServerSeqs`, which a cache can
  answer (verified Red against main's `client.go`: 3 → 0); the single-change
  loop is an `if`; the log names its size as YSON; the todo opens with its
  `**Created**` line. Deferred, as before: non-atomic purge/insert, the
  oversized document retried every cycle. Disputed: a size check on the push
  path (`CreateChangeInfos`). A pushed pack is bounded by `maxRequestBytes`
  (16 MiB, server/rpc/server.go) and a change's record carries little beyond
  its operations, so a single pushed change cannot reach the limit the way a
  whole-document fold does; out of scope here.

- Round 3 (CI panel on f1cb2c31). Correctness found that a conflict at the
  final `UpdateOne` came after the purge and the insert, so a push racing
  compaction across nodes left one change at seq 1 under a higher
  `server_seq`. The pre-purge `FindOne` only narrowed that window. Fixed by
  making the conditional document update the first write: without a
  transaction (MongoDB is not guaranteed to be a replica set) the only safe
  commit point is the single-document update every push also conditions on.
  The compacted change then overwrites seq 1 with one `ReplaceOne`, so the
  document reads consistently right after, and only cleanup remains. Not
  rolled back: a failed replace has an unknown outcome on a network error, so
  restoring the old `server_seq` could pair it with the new record.

## Panel round: the server-side size gate, re-raised a third time

The CI panel's security lens returned the gap that
`docs/design/document-size-limit.md` already records: `MaxSizePerDocument` is
sent to the client (`server/rpc/yorkie_server.go:360`) and enforced only in
`Document.Update` (`pkg/document/document.go:308`), so the server's push path
never re-checks it. The gap is real. It is not this branch's: the diff touches
no file on that path — not `server/rpc/yorkie_server.go`, not
`client/client.go`, not `pkg/document/document.go`, not
`server/packs/pushpull.go`.

An earlier version of this note said the new integration test does not
demonstrate the bypass. That was wrong. `d1.SetMaxSizeLimit(0)` only clears
the client's own gate, but the two `c1.Sync` calls then push 18 MB over the
real RPC path into a project whose quota is 10 MiB, and the server accepts
both: exactly what a modified SDK would do. The test does not open the gap; it
exercises it, because reaching a document larger than one MongoDB record is
the input the compaction path under test needs.

Rebutted rather than fixed, for the third time, on the grounds the design doc
states: closing it is a protocol change with an undecided refusal semantic.
`DocSize.Total()` is `Live + GC`, and deleting content moves bytes between them
without shrinking the total, so a blanket server-side refusal deadlocks any
document already over quota — the push that would delete content is refused for
the reason the push that added it was. The doc exists so that decision is made
once, in the open; making it unilaterally inside a compaction fix is the
re-litigation it was written to stop.

A fourth round returned the same finding, and three rebuttals in a row say the
answer "out of scope here" was incomplete: the gap had no owner, so every review
of anything near document accounting inherited it. It has one now.
`docs/tasks/active/20261003-server-side-document-size-gate-todo.md` carries the
blocking decision — which of the design doc's three refusal semantics — and the
work that follows from it, and `docs/design/document-size-limit.md` points at
that task. The finding stays open against it, not against this branch. The
design doc's references into `yorkie_server.go`, `client.go` and `document.go`
had drifted by a few dozen lines, which made the write-up read as stale; they
are corrected.

## Review round: panel (blast radius, correctness)

Two lenses, three blocking findings, all fixed in one pass.

**Compaction had no rollback.** `CompactChangeInfos` claims the document row
first (server_seq 1, epoch + 1) and writes the compacted change second, and
the two cannot share a transaction because the deployment is not guaranteed to
be a replica set. A failure between them left the document pointing at
server_seq 1 while the record there was still the pre-compaction first change:
wrong content, served silently, and out of reach of any later compaction,
since server_seq was already 1. `undoCompactionClaim` now restores `server_seq`
and `compacted_at` conditionally on the claim still standing, and joins its own
failure to the cause when it cannot. The epoch stays incremented on purpose —
lowering it could hand two different document states the same epoch, while a
spurious re-attach costs a round trip.

**The gate refused the repairs.** The lagging snapshot gate only knows a
document is over quota, so it refused `documents.UpdateDocument` and
`revisions.RestoreRevision` — both push `SetYSON` packs that `canGrow` calls
growing — on exactly the documents they exist to shrink. Both now measure the
root they already hold with `packs.CheckLiveSize` and set
`PushPullOptions.SizeChecked`, which skips the lagging gate for that push. An
exact check is available to them for free precisely because they build the
whole document; the push path's whole premise is that it does not.

**And it missed one write entirely.** `documents.CreateDocument` writes a
caller-supplied initial root through `DB.CompactChangeInfos` and never reaches
`pushPack`, so no quota applied to it at all. It calls `CheckLiveSize` too.
`docs/design/document-size-limit.md` now has a section naming all three paths,
so the next reader does not have to rediscover that the gate has a server-side
door.

## Review round 4 — panel (blast-radius, correctness, security)

Three blocking findings, all fixed.

**An ambiguous write error is not a failed write.** Round 3 added
`undoCompactionClaim` on the step-3 `ReplaceOne` error, which the panel caught
as worse than the hole it closed: a write-concern timeout or a retried command
errors on a write that is there, so the undo could restore `server_seq` to
`lastServerSeq` over an applied compacted record and leave the whole-root
change at seq 1 with changes 2..N still in place — a history every reader that
rebuilds the root replays on top of itself. `settleCompactionClaim` now reads
the record back and compares it element-for-element with the bytes that were
written (`rawEqualIgnoringID`, `_id` excepted because a replace keeps it)
before anything is undone. Landed means carry on to the purge; demonstrably
not landed means undo; a read that itself fails undoes nothing and returns
joined errors, because an unverified rollback is the one outcome that
corrupts. The repair also runs on `context.WithoutCancel` with its own
timeout — the failed write's likeliest cause is its context, which would make
every compensating write a no-op on that same context. The purge after a
landed-despite-error write runs on the detached context too.

**A revision ID is not a document.** `YorkieService.GetRevision` and
`RestoreRevision` authorized against `req.DocumentId` but then resolved the
revision from `req.RevisionId` alone — a cross-document, cross-project read
for the first and a write IDOR for the second, since `revisions.Restore`
derived the document to overwrite from the revision. Both now bind the ID to
the document: `revisions.GetForDoc` reports a revision belonging elsewhere as
not found, and `Restore` takes the `DocRefKey` to restore as a parameter, so
the document it writes is the one the caller authorized and locked. The admin
twins already did this check inline; the signature change makes it structural
rather than per-call-site.

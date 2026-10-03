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

## Panel round: the server-side size gate, re-raised a third time

The CI panel's security lens returned the gap that
`docs/design/document-size-limit.md` already records: `MaxSizePerDocument` is
sent to the client (`server/rpc/yorkie_server.go:360`) and enforced only in
`Document.Update` (`pkg/document/document.go:308`), so the server's push path
never re-checks it. The gap is real. It is not this branch's: the diff touches
no file on that path — not `server/rpc/yorkie_server.go`, not
`client/client.go`, not `pkg/document/document.go`, not
`server/packs/pushpull.go`.

The lens read the new integration test as demonstrating the bypass. It does not
open one. `d1.SetMaxSizeLimit(0)` is an in-process setter on the test's own
client object; a test that never leaves the process cannot show what an
untrusted client can do over the wire. The line is there to reach a document
larger than one MongoDB record, so the compaction path under test has an
oversized input, and the comment above it says so.

Rebutted rather than fixed, for the third time, on the grounds the design doc
states: closing it is a protocol change with an undecided refusal semantic.
`DocSize.Total()` is `Live + GC`, and deleting content moves bytes between them
without shrinking the total, so a blanket server-side refusal deadlocks any
document already over quota — the push that would delete content is refused for
the reason the push that added it was. The doc exists so that decision is made
once, in the open; making it unilaterally inside a compaction fix is the
re-litigation it was written to stop.

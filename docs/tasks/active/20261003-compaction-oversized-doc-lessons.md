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

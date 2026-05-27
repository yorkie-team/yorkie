**Created**: 2026-05-27

# Flaky Test Fixes: Bench Panics in GetDocuments and ListWhileModifying

Branch: `fix/bench-flaky-panics`

## Goal

Eliminate two pre-existing panics in `test/bench` that intermittently
break CI. Both are bugs in the bench test code itself, not in
production code.

1. `BenchmarkGetDocuments/with_root_presence_1000` panics with
   `nil pointer dereference` at
   `test/bench/get_documents_bench_test.go:60`.
   Seen on CI run 26515547459 (commit 449b2c44).
2. `BenchmarkChannelConcurrency_ListWhileModifying/50r_10w` panics
   with `close of closed channel` at
   `test/bench/channel_concurrency_bench_test.go:446`.
   Seen on CI run 26515489677 (commit 098b521f). Previously reported
   as #1703 and closed under the assumption that PR #1745 had fixed
   the underlying race — but #1745 only adjusted server-side lock
   order in `pushpull.go`; the test-side race was never patched and
   has now reoccurred.

## Root causes

### A. Nil response dereference in GetDocuments bench

`get_documents_bench_test.go` uses `assert.NoError(b, err)` followed
by `assert.NotNil(b, resp.Msg)`. `assert.NoError` is non-fatal — it
records the failure on `b` but lets execution continue, so when the
RPC returns `(nil, err)` the next line dereferences a nil `resp`
and SEGVs.

The trigger is `with root presence 1000`, where
`documents.GetDocumentSummaries` spawns 1000 cluster-RPC goroutines
per benchmark iteration multiplexed over a single HTTP/2 connection
(default `ClusterClientPoolSize=1`). Under sustained `b.Loop()`
pressure a transient cluster RPC error eventually surfaces. The
correct test behavior is to fail cleanly with the RPC error, not to
panic.

### B. Race on `close(done)` in ListWhileModifying

Each writer goroutine does:

```go
atomic.AddInt32(&writeCount, 1)
if atomic.LoadInt32(&writeCount) >= int32(tc.writers) {
    close(done)
}
```

Several writers can pass the threshold concurrently and call
`close(done)` more than once. The same file already guards the
identical pattern in `BenchmarkChannelConcurrency_SessionCountWhileModifying`
with `var closeOnce sync.Once`; `ListWhileModifying` is a copy-paste
of that block that lost the guard.

## Plan

- [ ] Apply fixes:
  - [ ] `get_documents_bench_test.go`: use `require.NoError` (or
        `b.Fatal`) so an RPC failure stops the goroutine instead of
        falling through to a nil deref. Apply at all call sites in
        `benchmarkGetDocuments` and in setup.
  - [ ] `channel_concurrency_bench_test.go`: add `var closeOnce sync.Once`
        in `ListWhileModifying` and wrap the `close(done)` in
        `closeOnce.Do(...)`, matching `SessionCountWhileModifying`.
- [ ] Verify: `make lint` green; sanity-run the two bench subtests
      locally with `-count` to confirm the panics are gone.
- [ ] Self-review via code-review subagent.
- [ ] Open PR, link issue #1703 for context.

## Notes

- The bench RPC concurrency story (1000 goroutines per iter) is a
  separate optimization question. This change only makes the test
  fail gracefully when an error does occur; it does not try to
  prevent the error.
- Production code is not modified.

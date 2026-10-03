**Created**: 2026-10-03

# Compacting a document past MongoDB's record limit

## Problem

Compaction folds a whole document into one change record. When the document
is large, that record exceeds MongoDB's 16 MiB document limit and the insert
fails. Three things go wrong at once:

1. **Data loss.** `CompactChangeInfos` purges the document's changes,
   snapshots and version vectors first and inserts the compacted change
   second. A failed insert leaves the document with no changes and no
   snapshot. Reproduced in an integration test: 4 stored changes before the
   failed compaction, 0 after. A server that still holds the document in its
   caches keeps answering with the old content, which hides the loss until
   those caches are dropped.
2. **A 20 MB log line.** The `[CD] ... failed to compact` error log prints the
   whole root (`Root: %s`), so the failure writes the document's content into
   the log as a single line.
3. **CI bench hangs for 6 hours.** In `BenchmarkRPC/attach large document`
   (two 10 MB texts under one key), housekeeping compacts the document while
   the bench runs. The 21M-character log line makes the Actions runner's
   problem matchers time out ("Removing issue matcher 'eslint-compact' ... The
   Regex engine has timed out"), the step's log stops, and the job sits until
   the 360-minute default. Under `-bench`, `go test` arms neither its
   `-timeout` alarm nor its kill timer, so nothing inside the step ends it.

Bench runs started hanging on 2026-09-27, and only on the element-RHT
slot-fix branches (#2081, #2100, and their predecessor). On main the same bench
document loses its content to the stranded-slot bug those branches fix (a
fresh attach reads `{}`), so compaction there writes an empty change and
never reaches the limit. With the fix, the content survives and compaction
hits the limit.

## Plan

- [x] Failing integration test: an 18 MB document (two 9 MB pushes) is
      compacted; expect `ErrChangeTooLarge` and the stored change count
      unchanged
- [x] Encode the compacted change with the client's registry and check it
      against 16 MiB before purging; insert those exact bytes
- [x] Drop the root from the `[CD]` error log; log its size
- [x] Bound the CI bench job with `timeout-minutes: 60`
- [x] `make lint`, `go test ./...`, compaction integration tests
- [x] Review follow-up: check `server_seq` before the purge (shared
      `RunCompactChangeInfosTest`, memory and mongo), count stored changes
      in the integration test straight from MongoDB
- [x] Review round 3: claim the document (conditional `server_seq` update)
      before touching its changes, replace the change at the new seq in one
      write, then drop the rest; a refused claim touches nothing. Missing
      document reports `ErrDocumentNotFound` as in memory. Shared tests check
      snapshots and version vectors survive a refused compaction, plus a
      mongo-only two-node test (compaction on one, stale push on the other)

## Out of scope

- Compacting such a document at all. It now fails cleanly every
  housekeeping cycle and stays uncompacted.
- The bench reusing one key across `b.Loop` iterations, and the stranded-slot
  bug on main (fixed on the element-RHT branches).

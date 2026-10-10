**Created**: 2026-10-10

# Park rejected writes lessons

- A server-side sentinel does not survive the wire as itself. The client
  sees a `connect.Error`, so `errors.Is(err, document.ErrDocumentSizeExceedsLimit)`
  is false; classify by `converter.ErrorCodeOf(err)` against the sentinel's
  `Code()`.
- Parking does not make a refused document recoverable. The server refuses
  a pack that holds any growing change, and the refused change stays queued,
  so deleting content afterwards does not help. Say what a port does not
  fix before the reviewer has to.
- State that records the outcome of a call guarded by a lock belongs inside
  that lock. Storing it after the unlock let a concurrent success be
  overwritten by an older failure.
- Never stash or edit in a worktree while a test run reads it; the run's
  result is then meaningless and has to be repeated. This happened twice:
  once by stashing, once by reading `test=0` from a `cat` of the previous
  run's output and editing while the new `make test` was still going --
  which `make verify` then exposed as a port collision. Wait for the
  completion notice, and print a run's own exit code, not an earlier log.

## Self review

- Round 1 (correctness/tests): see the todo's Review. Five fixed, two left
  as known limitations with reasons.
- Round 2 (design fit/docs): three stale docs and an over-built field
  (atomics + clock anchor where `syncMu` already guards access) fixed.
  Match the guard the neighbouring fields use before reaching for atomics.

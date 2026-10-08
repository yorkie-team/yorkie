# Lessons — Skip auto revisions when only presence changed

**Created**: 2026-10-08

## What the code already told us

- `storeSnapshot` already has everything needed to detect a presence-only
  interval: `changes` covers the span since the previous snapshot. No new
  bookkeeping, no new query.
- The condition the issue proposed — `len(changes) == 0` — would have been a
  no-op. `FindChangeInfosBetweenServerSeqs` (mongo `client.go`) fills a
  temporary store from the in-memory presence cache *before* reading the
  `changes` collection, so a presence-only interval comes back non-empty
  whenever that cache is warm, which is exactly the case the fix targets.
  The property the "the revision cannot differ" argument actually rests on is
  "no change in the interval carries an operation", so that is the gate:
  `hasOperations(changes)`.
- The revision content is `yson.FromCRDT(doc.RootObject())` — the visible
  root. That is why garbage collection running inside `ApplyChangePack`
  cannot smuggle a content difference into an otherwise empty interval: GC
  only removes tombstoned nodes, which the visible root never contained.

## Decisions

- Guarded at the call site rather than inside `storeRevision`. `storeRevision`
  is about "does this project want revisions"; "is there anything new to
  record" is the caller's knowledge, and passing `changes` down just to count
  it would widen the function's contract.
- Left Proposal 2 alone. The issue asks for discussion on it, and the two
  candidate designs (skip the snapshot; count the interval in operation
  changes) have different costs — an extra range query per push versus a
  change in what `SnapshotInterval` means. Picking one silently would be
  substituting a decision for the maintainers'.

## Verification gap

- `make test` needs a MongoDB stack and is out of reach for this run, so the
  new integration test was not executed locally; only `go vet -tags
  integration` covers it. `make lint`, `make verify-license`,
  `make verify-modernize` and `go test ./server/packs/...` are green. CI runs
  the integration lane on the PR.

## Self review

- Not run. This run has no tool that can dispatch the reviewer subagent, so
  the round was skipped rather than substituted with a self-read. Reviewers
  are CI, `@claude review`, and a human.

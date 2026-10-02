# Lessons: push-only reply GC

## Reproduction

- `TestPushOnlyGarbageCollection` (port of JS `pushonly_gc_test.ts`) fails on
  `main` at the final full pull with `node not found`, the Go form of the JS
  `ChangeApplyError … cannot find node`. With the fix it passes.
- Explicit `Sync(WithKey(k).WithPushOnly())` on a manual attachment gives a
  deterministic push-only round trip; no need for realtime mode and polling as
  the JS test does.

## Why only GC

- The Go `Document` reads a reply's version vector in two places only:
  `applySnapshot` and `garbageCollect`. A push-only reply carries neither
  changes nor a snapshot, so taking it as an ack drops exactly the GC.
- The disable-GC path (`SetClocks` from a one-entry vector) is server-side
  and applies to snapshot pulls only; a push-only reply never reaches it.

## Environment

- `make lint` with a golangci-lint built by Go 1.27 fails on `internal/goarch`
  export data. Run tools and verify with `GOTOOLCHAIN=go1.26.0` (CI pins 1.26).
- Running `make verify` and `make test` at once starved MongoDB server
  selection (`TestDocRootChangedEventWebhook` ping timeout); run them in turn.

## Self review

Reviewer: harness `/code-review high` over `origin/main...HEAD` (not the CI
lens panel).

- Round 1 (correctness/tests): 7 findings. Fixed 5: `syncInternal` reset
  `changeEventReceived` after a push-only sync that pulled nothing (now only
  after a pull); unit test `TestAcknowledgePushedChanges` so the commit gate
  covers the ack path; the triplicated local-change trim is now
  `InternalDocument.removePushedLocalChanges`; `Checkpoint.SyncClientSeq`
  instead of a hand-built `Forward`; the integration test asserts
  `GarbageLen` is unchanged across the push-only reply. Deferred 1, disputed 1
  (below).
  - Deferred: "the server should not attach minVV to a push-only reply"
    (`server/packs/pushpull.go:419`). Sound defence for clients without this
    fix, but it changes the reply of every SDK; raised as a follow-up in the
    PR body rather than folded into a client port.
  - Disputed: "AcknowledgePushedChanges is a new exported method … consider
    unexporting it" (`pkg/document/document.go`). The caller is in package
    `client`, so it must be exported; `ApplyChangePack` and
    `CreateChangePack` are exported for the same cross-package reason and
    are equally misusable. The doc comment names the push-only reply as its
    only use.
- Round 2 (design fit/blast radius): 9 findings. Fixed 3: client unit test
  `TestPushOnlySyncKeepsPullSignal` pins the round-1 `changeEventReceived`
  rule (fails when the guard is removed); the client NOTE now points at the
  design doc instead of restating it; the unit test says why its server seq
  is ahead. Deferred 1 to the PR body: this client patch alone does not
  protect older clients or other SDKs (same as the round-1 server deferral).
  Disputed 5:
  - "removePushedLocalChanges … keeps acked changes reachable; nil the
    dropped slots" (`internal_document.go:159`). Tried and reverted:
    `InternalDocument.DeepCopy` (`internal_document.go:556`) shares the
    `localChanges` slice with the copy, so clearing slots would hand the copy
    nil changes. The re-slice is the pre-existing behavior, moved.
  - "AcknowledgePushedChanges is a second copy of applyChangePack's steps;
    use applyChangePack(pack, pulled bool)" (`document.go:768`). The two
    differ in more than GC: the ack applies no changes or snapshot and must
    not forward the server seq. A flag would gate three of five steps. The
    shared step (trim) is already one helper, and JS keeps the same split
    (`acknowledgePushedChanges` beside `applyChangePack`).
  - "AcknowledgePushedChanges drops pack.Changes/Snapshot without logging"
    (`document.go:772`). The server builds the push-only reply with nil
    changes and snapshot (`server/packs/pushpull.go:446`), and dropping is
    safe since the unmoved server seq re-pulls them. `pkg/document` has no
    logger to report through.
  - "Fold the integration test into gc_test.go" (`pushonly_gc_test.go:41`).
    Kept beside its JS twin's name (`pushonly_gc_test.ts`) so the two SDKs'
    regressions can be found by name; gc_test.go's push-only case pins
    vector bookkeeping, not this convergence.
  - "Commit 98c087fa mixes refactor and fix under one subject". The repo
    squash-merges PRs (`git log` subjects end in `(#N)`), so per-commit
    bisect granularity does not survive into `main`.

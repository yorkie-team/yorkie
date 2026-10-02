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

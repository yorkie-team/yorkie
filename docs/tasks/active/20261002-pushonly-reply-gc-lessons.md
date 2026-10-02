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

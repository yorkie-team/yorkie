# Unify client watch event delivery into a single path

**Created**: 2026-07-03

## Goal

Fix the root cause of flaky
`TestDocumentWithProjects/watch_document_with_different_projects_test`
(#1847): `DocumentWatched`/`DocumentUnwatched` can be delivered out of
order because two goroutines race on the same watch response channel.

## Root Cause

Presence lifecycle events reach the user-facing `rch` channel via two
independent paths in `client.runWatchLoop`:

1. Stream path: server `DocWatched`/`DocUnwatched` → `handleWatchResponse`
   → reconcile → stream goroutine sends directly to `rch`.
2. Sync path: PushPull applies presence changes → `ApplyChanges` →
   `ReconcilePresence` → `d.events` → forwarder goroutine sends to `rch`.

When c2's watch event arrives before its presence syncs, the
`WatchedEvent` is emitted later via path 2 while the `UnwatchedEvent`
goes via path 1 — two goroutines race on unbuffered `rch`, reordering
events ~12% of runs.

## Design

`Document` becomes the single publisher of presence lifecycle events.
See `docs/design/doc-presence.md` — "Presence Event Delivery
Unification" section.

- `AddOnlineClientAndReconcile`/`RemoveOnlineClientAndReconcile` emit
  the reconciled event into `d.events` while holding `d.mu` (same
  pattern as `applyChanges`), instead of returning it. Lock order =
  state transition order = channel send order.
- `handleWatchResponse` returns `nil, nil` for `DocWatched`/
  `DocUnwatched`; the `d.Events()` forwarder becomes the sole source
  of watched/unwatched/presence-changed on `rch`.

## Checklist

- [x] Update `docs/design/doc-presence.md` with delivery unification
      section
- [x] `pkg/document/document.go`: reconcile methods emit into
      `d.events` under lock, drop return value
- [x] `client/client.go`: `handleWatchResponse` stops sending
      watched/unwatched directly
- [x] Keep `document_test.go` order-sensitive assert as regression
      guard
- [x] Self-review round 1: found send-under-lock deadlock class
      (producer under `d.mu` → cap-1 `d.events` → forwarder → `rch` →
      consumer calling `Update`), stranded reconcile send on teardown,
      and dual-forwarder panic on reconnect
- [x] Address review: unbounded `watchBuffer` + pump/sender/stream
      reader pipeline in `runWatchLoop`; pump stops only after stream
      reader exits; sender is sole writer/closer of `rch`
- [x] Fix test timing assumption surfaced by the pipeline: the
      unthrottled stream reader can process unwatch before presence
      syncs → no events by design (JS parity); the test now waits for
      `watched` before unwatching
- [ ] Verify: 20+ repeated runs of flaky + regression tests,
      `make lint`, `go test ./...`, full integration suite
- [ ] Open PR referencing #1847

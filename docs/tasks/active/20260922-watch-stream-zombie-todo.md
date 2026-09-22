**Created**: 2026-09-22

# End the Watch stream when its subscriptions close

**Issue**: yorkie-team/yorkie#2024. **PR**: yorkie-team/yorkie#2025.
Branch `fix/watch-stream-zombie`, based on `main` @ `1384ba7a`.

## The defect

`streamMergedEvents` (`server/rpc/yorkie_server.go`) spawns one fan-in
goroutine per subscription and reads what they merge into a single channel.
Each fan-in returns when its subscription's event channel closes — but nothing
closed `merged`, so once the last fan-in returned the handler blocked on a
channel no one could write to. The client stayed connected and believed it was
watching; no event could reach it and no error was returned.

It is a regression from #1666, which replaced `WatchDocument`/`WatchChannel`
with the unified `Watch` RPC. The helper those two still use,
`streamEvents` (`server/rpc/stream.go:46`), has carried
`case event, ok := <-sub.Events(): if !ok { return nil }` all along. The fan-in
rewrite dropped that handling; this branch restores it for the merged path.

## How a subscription reaches the closed state

`Subscription.Publish` (`server/backend/pubsub/subscription.go:127`) closes its
own event channel after `maxConsecutivePublishFailures` (100) consecutive
failed sends, each waiting `publishTimeout` (100ms). Added in #1833 as the only
cleanup path for subscriptions whose stream handler never unsubscribes.

The pipeline in front of it is shallow, which is what makes the trigger
reachable: a doc subscription buffers **1** event
(`pubsub/doc_subscription.go:35`), `merged` buffers one slot per subscription
(one, for every stream the Go SDK opens), and the `BatchPublisher` window is
100ms. A client that stops reading fills all of it within a few events, so on
an active document the threshold arrives in roughly 10-20 seconds — a
backgrounded tab, a stalled mobile connection, a paused debugger.

## What the zombie cost beyond the dead stream

The handler's main loop also selects on `ctx.Done()`, so it did unblock when
the client disconnected. Everything in its deferred cleanup was therefore
delayed until disconnect rather than skipped — and two of those matter:

- `unwatchDoc` (`yorkie_server.go:1700`) is the **only** publisher of
  `DocUnwatched`. The `BatchPublisher` reap
  (`pubsub/batch_publisher.go:141-168`) deletes the pruned subscription from
  the map but publishes nothing, so every peer kept listing the pruned client
  as an online watcher for as long as the zombie lived.
- `watch_document_stream_connections_total` stayed incremented, so the gauge
  counted streams that could not deliver an event.

## Approach

1. Track the fan-in goroutines with a `sync.WaitGroup` and close `merged` once
   the last one returns; the main loop returns `nil` on `!ok`. Ordering is what
   makes this safe: `defer wg.Done()` runs after that goroutine's last send, so
   nothing can send on a closed `merged`, and a closed buffered channel yields
   its queued events before reporting the close, so no event is dropped. Both
   fan-in paths already select on `done` at their send, so `wg.Wait()` cannot
   hang and the waiter goroutine cannot leak.
2. `streamMergedEvents` takes `send func(*api.WatchResponse) error` instead of
   `*connect.ServerStream[api.WatchResponse]`, which was the only thing it used
   the stream for. Same signature `streamEvents` already takes, and it makes
   the channel lifecycle testable with no network stack.
3. Reject a `Watch` that would subscribe to nothing — in both spellings. An
   empty `Resources` list gives `ErrNoResources`; a descriptor whose resource
   oneof is unset gives `ErrUnsupportedResource`, rejected rather than skipped
   even beside a valid descriptor. Without the first commit such a request only
   blocked until disconnect; with it the stream closes right after
   initialization, which a client may read as an unexpected termination and
   retry in a loop.

## Checklist

- [x] Close `merged` when the last fan-in returns; return `nil` on `!ok`.
- [x] `send func(...) error` in place of the concrete server stream.
- [x] `TestStreamMergedEventsEndsWhenSubscriptionsClose` — the single
      subscription case, and that the stream stays open while any subscription
      is still live.
- [x] Reject the empty `Resources` list (`ErrNoResources`).
- [x] Reject a descriptor that names no resource (`ErrUnsupportedResource`),
      with `TestStreamMergedEventsEndsWithoutSubscriptions` pinning why such a
      stream has to be refused.
- [x] Error vars in a block below the imports, per the rest of the repository.
- [x] `make lint` (0 issues), `go test ./...`, the target tests under `-race`,
      `make test` with MongoDB up.
- [ ] Address the review on #2025 and merge.

## Known limitations, carried in the PR body

- **A partial prune is still silent.** The stream ends only when *every*
  subscription has closed. If one of several closes while the others stay live,
  that one resource is dead for the life of the stream with nothing saying so —
  the same class of defect, one layer in. Today the Go SDK opens one Watch per
  resource, so nothing in-tree hits it; the RPC accepts a list, so something
  will. Closing it properly wants a per-resource signal in `WatchResponse`,
  which is a protocol change and not this fix.
- **`ErrUnsupportedResource` fails a mixed request whole.** A newer SDK naming
  a resource type this server does not know has its entire `Watch` rejected
  rather than watching the descriptors the server does recognise. Deliberate: a
  partial watch the client cannot detect is the failure this branch exists to
  remove. Serving the recognised ones and naming the dropped ones wants the same
  per-resource field in `WatchInitialization` the limitation above wants.
- **`return nil` ends the stream gracefully, and the Go client only reconnects
  on an error.** `client/client.go:1029` re-establishes the watch loop when
  `stream.Err() != nil` and simply closes its buffer otherwise. So a self-prune
  now stops the application's watch channel with no error rather than
  recovering it. That is the convention `streamEvents` already set for this
  condition, and it is a strict improvement on silence — the closed channel is
  a signal the application can see — but if recovery is wanted, a retriable
  code (`Unavailable`) would ride the reconnect path that already exists. A
  question for the maintainers, not a change this PR makes on its own.

## Not in scope

The trigger end to end: a client slow enough to time out `Publish` 100 times
in a row, then resuming. HTTP/2 flow-control buffering lets the handler keep
draining well before the subscription buffer fills, so driving it
deterministically through a real stream is awkward. The tests drive the closed
state directly, and the fix does not depend on how the subscription came to be
closed.

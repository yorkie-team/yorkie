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
   the last one returns; the main loop returns `ErrSubscriptionsClosed` on
   `!ok` — a retriable `Unavailable`, so the SDK re-establishes the watch
   instead of reading a stream it never ended as complete. Ordering is what
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
4. Bound the resource list from above as well. Every descriptor costs a
   database read, a subscription slot and a goroutine for the life of the
   stream, so `maxWatchResources` (100) caps what one request can make the
   server hold.
5. Ask the auth webhook about the resources the stream would deliver. The
   request carries document ids and only the key identifies a document to a
   webhook, so descriptors are resolved to their keys before `VerifyAccess`
   and passed as `AccessAttributes`. The deprecated `WatchDocument` and
   `WatchChannel` shims get the same treatment — leaving either unresolved
   would keep a way around per-resource authorization.
6. Re-establish the client's channel watch when a stream it did not close
   ends. `Client.WatchChannel` dropped its `countChan` on any stream end, so
   the channel stopped delivering broadcasts and session counts for the rest
   of the client's life.

## Checklist

- [x] Close `merged` when the last fan-in returns; return
      `ErrSubscriptionsClosed` on `!ok`, and the same from `streamEvents` so
      the deprecated shims report a self-prune the same way.
- [x] `send func(...) error` in place of the concrete server stream.
- [x] `TestStreamMergedEventsEndsWhenSubscriptionsClose` — a document
      subscription, a channel subscription, and that the stream stays open
      while either is still live. Both fan-in loops are separate copies, so
      both need covering.
- [x] `TestStreamMergedEventsDeliversQueuedEvents` — an event already in
      `merged` reaches `send` before the close is reported.
- [x] Reject the empty `Resources` list (`ErrNoResources`).
- [x] Reject a descriptor that names no resource, a nil descriptor, and a list
      longer than `maxWatchResources`, with
      `TestStreamMergedEventsEndsWithoutSubscriptions` pinning why a stream
      with no subscription has to be refused, and
      `RunWatchResourceRejectionTest` pinning that each rejection reaches the
      client as a status rather than as a stream that opens and closes.
- [x] Resolve descriptors to keys and pass them to the auth webhook, with
      `TestAuthWebhookWatchAttributes` pinning it end to end.
- [x] Re-establish `Client.WatchChannel` after a stream end it did not ask
      for, and only after one that delivered something, so a server ending
      every stream at once cannot spin the loop.
- [x] Document the `resources` contract in `yorkie.proto`, and the new stream
      lifecycle in `docs/design/pub-sub.md`.
- [x] Error vars in a block below the imports, per the rest of the repository.
- [x] `make lint` (0 issues), `go test ./...`, the target tests under `-race`,
      `make test` with MongoDB up.
- [x] Address the review on #2025.
- [ ] Merge.

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
- **Resolving resources before authorization moves a database read ahead of
  the webhook.** A client the webhook would deny now costs one
  `FindDocInfoByRefKey` per descriptor, and a `NotFound` tells it whether a
  document id exists in the project. `Watch` already read `clients` before
  authorizing, and the alternative — asking the webhook twice, once without
  attributes and once with — doubles webhook load on every stream. Bounded by
  `maxWatchResources`, and strictly better than the state it replaces, where
  the same client received the document's events.
- **Auth webhooks see `Watch` attributes they did not see before.** A
  deployment whose webhook rejects input it does not recognise would start
  denying watches it used to allow. That is the point of the change, but it is
  a behaviour change for self-hosted webhooks and belongs in the release note.
  The webhook cache keys on the marshalled request (`auth/webhook.go:59`), so
  `Watch` entries also go from one per token to one per token and resource.
- **`maxWatchResources` is a constant, not a project setting.** 100 is far
  above anything the SDKs do today (one resource per stream). If a client ever
  multiplexes more than that, this becomes a project limit alongside
  `MaxSubscribersPerDocument` rather than a recompile.

## Not in scope

The trigger end to end: a client slow enough to time out `Publish` 100 times
in a row, then resuming. HTTP/2 flow-control buffering lets the handler keep
draining well before the subscription buffer fills, so driving it
deterministically through a real stream is awkward. The tests drive the closed
state directly, and the fix does not depend on how the subscription came to be
closed.

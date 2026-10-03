**Created**: 2026-09-22

# Lessons: the Watch stream that never ended

Captured while fixing yorkie-team/yorkie#2024. The plan is in
`20260922-watch-stream-zombie-todo.md`.

## The old helper was the specification, and it was still in the tree

`streamEvents` has handled a closed subscription with
`if !ok { return nil }` since before the unified `Watch` RPC existed, and the
deprecated `WatchDocument`/`WatchChannel` shims still call it. The fan-in
rewrite in #1666 did not decide to behave differently; it reimplemented the same
loop for N subscriptions and lost one case. Both directions of that are worth
keeping: the diagnosis was cheap because the correct version of the loop was
sitting in `server/rpc/stream.go` the whole time, and the fix needed no new
convention — including the `send func(...) error` parameter, which is the
signature `streamEvents` already takes.

The general shape: when a handler is generalised from one resource to many, the
single-resource version is the oracle for the new one. Diffing against it finds
dropped cases faster than reading the new code for bugs.

## One spelling of a condition is not the condition

The first attempt at the guard read `len(req.Resources) == 0`. That is the
obvious spelling of "this stream will subscribe to nothing" and it is not the
only one: a `ResourceDescriptor` whose resource oneof is unset falls through the
type switch with no case to match, so `Resources: [{}]` passed the guard,
subscribed to nothing, and — because the first commit had just made a
subscription-less stream end immediately — closed right after initialization.
The guard prevented the retry loop for the empty list and created it for the
malformed one. CodeRabbit flagged the same gap on the PR, independently.

What made it visible was asking what the *state* being rejected is, not what the
bad input looks like. `subscribeResources` now carries that invariant in its doc
comment — on success, at least one subscription exists — and the switch has a
`default` that refuses anything it cannot subscribe to. The version keyed on
input shape would have needed a third amendment the day a third resource type
ships; the version keyed on the state does not.

The same reasoning settled what to do about a bad descriptor sitting beside a
good one. Skipping it leaves a client that asked for two resources watching one,
with no way to learn which — which is the silence this whole branch is about, so
the descriptor is rejected even when the rest of the request is fine.

## What a delayed defer costs is not obvious from the defer

The issue was written as "the handler blocks forever", and the handler's main
loop also selects on `ctx.Done()`, so it does return on disconnect. The
interesting question was therefore not whether cleanup runs but what it is
holding, and one item in that deferred block was worth more than the stream
itself: `unwatchDoc` is the only publisher of `DocUnwatched`. The
`BatchPublisher` reap removes the pruned subscription from its map and publishes
nothing, so every *other* client in the document kept the pruned one in its
peer list until the zombie's client disconnected. A bug filed as "one client
stops receiving events" was also "every other client shows a ghost".

Worth reading a deferred cleanup as a list of invariants held open, not as
housekeeping — the gauge in the same defer was the cosmetic item, and it was the
one easiest to notice.

## Buffer sizes decided whether the trigger was theoretical

The self-prune threshold is 100 consecutive failures at 100ms, which reads like
something a real client would never reach. What decides it is how deep the queue
in front is: a doc subscription buffers **one** event, `merged` one slot per
subscription, and the publisher batches every 100ms. Three events in flight,
then every publish times out. Ten to twenty seconds of a stalled reader on an
active document, not hours.

The reachability argument lives in those three constants, and none of them is
near the code that prunes. Recording them next to the threshold is what turned
"in principle" into a severity.

## A graceful end is not the same as recovery

`return nil` closes the stream cleanly, and the Go client reconnects only when
`stream.Err() != nil`; on a clean end it just closes the application's channel.
So the first version of the fix converted silence into a visible stop, not into
a recovered watch — and it was recorded that way, as a known limitation, since
"the handler returns" and "the client keeps watching" are different claims.

The review made it a blocking finding, correctly: the whole reason the handler
was worth fixing is that a slow-but-live client loses its watch, and a stop it
cannot recover from is the same outcome one layer over. What made the
limitation look acceptable was that `streamEvents` already did it — but a
convention is only evidence about intent, not about correctness, and here it
was the older half of the same bug. The lesson is to distrust "the existing
code already does this" exactly when the existing code is what is being fixed.

The fix is a status, not a mechanism: `ErrSubscriptionsClosed` is
`Unavailable`, which both client watch loops already treat as retriable. A
self-prune is a server-side decision the client never asked for, so it has to
read as "try again", never as "you are done".

## Process: mutation-check against a committed baseline

Both fixes were confirmed by removing them and re-running the tests: without
the `WaitGroup` both lifecycle subtests fail after their own 5s timeouts, and
without the `default` case the unset-descriptor subtest reports `got nil` and
code `ok`. That is the check worth having — a test that passes only because it
tests nothing looks identical to a passing test otherwise.

Doing it with `git checkout --` to restore, on a fix that was not committed yet,
threw the fix away instead of the mutation. Commit first, or mutate a copy.

## Review rounds

- **Round 1 (correctness / tests).** A verification pass over the full branch
  diff: the trigger path, the ordering argument for the new `WaitGroup`, the
  before/after behaviour of both tests under mutation, and `make lint` +
  `go test ./...` + the target tests under `-race`. Findings: the
  `len(Resources)` guard missed the unset-oneof spelling (fixed, `5ff4597c`);
  error vars declared mid-file against repository convention (fixed, same
  commit); the partial-prune gap and the no-reconnect-on-clean-end behaviour
  recorded as known limitations in the PR body rather than fixed here.
- **Rounds 2 (design fit) and 3 (security / docs): not run before the PR.**
  This was a targeted verification pass, not the bounded `/self-review` loop,
  and a round that did not happen must not read as a clean one. CodeRabbit's
  automated review found exactly one issue — the same unset-oneof gap — which
  is corroboration on correctness, not a substitute for the two rounds.
- **Round 2 (the review panel on #2025, five lenses).** The rounds that were
  skipped are the ones that found something: four of the five blocking
  findings came from the design-fit, security and blast-radius lenses, and the
  fifth from test adequacy. Resolved as follows.
  - *Clean end gives the SDK no error.* Fixed — see above.
  - *Lifecycle tests only build `channelSub`.* Fixed. The two fan-in loops are
    separate copies of the same body, so covering one proved nothing about the
    other; dropping `wg.Done()` from the document loop now fails a test.
  - *Resource list bounded only from below.* Fixed with `maxWatchResources`.
    Adding the lower bound and not the upper one was the tell: the guard was
    written to answer "can this stream deliver anything?", and nobody asked
    the adjacent question.
  - *Auth webhook called with no attributes.* Fixed by resolving descriptors
    to keys before `VerifyAccess`. Pre-existing, and the reviewer said so —
    but a pre-existing gap on the exact path a diff rewrites is still that
    diff's to close, and fixing only the unified `Watch` would have left the
    deprecated shims as a way around it.
  - *Client `WatchChannel` never re-establishes.* Fixed. Also pre-existing:
    it dropped its `countChan` on a network error too, so no channel watch
    ever survived a disconnect.
  - Pushed back on nothing. Two findings turned on facts the branch's own
    notes had already recorded as limitations, which is not a defence.
- **#2084.** A lifetime mismatch is rarely fixed by synchronising the
  handoff: the object with the longer life (the attachment) has to own the
  resource (the event pump).
- "Cancel and join" is a complete shutdown only if the thing joined is the
  last producer. Stop the producers first, the consumer last; and waiting on
  a goroutine inherits every lock it takes.
- A reconnect test must hold the window open on the server; publishing after
  a full round trip tests nothing.
- Never roll back local state for something the server already committed. A
  failed initial Watch that dropped the attachment left the server attached
  and the caller unable to Detach or re-Attach.
- Stopping the sync loop is not the same as stopping synchronisation.
  `Deactivate` tore every pipeline down arguing only that the loop was
  joined, but `Client.Sync` is public: a user goroutine could be inside
  `ApplyChangePack` with the attachment's `syncMu` held. The fix is the one
  `Detach`, `Remove` and `pushPullChanges` already use — retire the pump
  under `syncMu` — plus a `statusDeactivating` state so a sync that has yet
  to reach `pushPullChanges`' guard is rejected instead of racing it. A
  guard checked once at entry does not hold for the rest of the call.
- A new intermediate state is a new set of edges, not one. `statusDeactivating`
  was added for the guards that read `!= statusActivated` and left every other
  reader of the field untouched: `Activate` would lay a new ID over the ended
  session's attachments. A transient state needs an answer for every entry
  point that observes it — and because it is observed by goroutines that never
  wrote it, the field itself has to be atomic for the window to mean anything.

## Review rounds (continued)

- **Round 3 (the review panel on #2084, correctness + security).**
  - *A failed `Deactivate` restored `statusActivated`.* The first attempt at an
    "exit on the error path" picked the wrong exit. The restore was reasoned
    about as a status question — a session the server still holds should read
    as live — when it is a resource question: `Deactivate` retires every
    pipeline *before* the RPC, and a retired pump does not come back. Restoring
    `statusActivated` re-opened `Sync` on documents with no consumer for their
    events, so the next `ApplyChangePack` would wedge on the capacity-one event
    channel holding the document's event mutex. The exit from a transient state
    has to leave the invariants the state was entered to establish intact; when
    the entry destroyed something, there is no way back, only forward. The
    client now stays deactivating and `Deactivate` is the one call it still
    admits, so a retry is the way out.
  - *Two `Deactivate`s could interleave their status writes.* A status read at
    the top of a call does not survive an RPC in the middle of it. Serialised
    on `deactivatingMu` and re-read under the lock.
  - *`AuthInterceptor.token` stayed a plain field.* The delta's own stated
    invariant — a field read by goroutines that never wrote it is accessed
    atomically — was applied to `status` and not to the credential beside it,
    which `SetToken` swaps at runtime while every watch and sync goroutine
    reads it. An invariant written down for one field is a checklist for the
    rest of the struct.

- **Round 4 (the review panel on #2084, blast radius + correctness).**
  - *The deactivating guard covered one half of `syncInternal`.* The guard was
    added to `pushPullChanges` because that is where the wedge was reasoned
    about, and `refreshChannel` — the channel half of the very same
    `syncInternal` — was left open, so a user-goroutine `Client.Sync` on a
    channel still reached the server inside the window. A guard belongs to the
    fan-out point's every branch, not to the branch the bug was found in.
  - *"Retry until it succeeds" is only an exit for a failure that can stop
    happening.* Round 3 answered the failed-`Deactivate` problem with a state
    whose only exit was a successful `Deactivate`; a session the server has
    already dropped fails identically forever, so the client was bricked and
    `Close` returned the error before it ever released the connection. `Close`
    is now the terminal disposal: it gives up on the server-side session and
    finishes the local half itself. A recovery path has to work for the
    permanent case, not just the transient one.
  - *Build the consumer before the producer, everywhere.* The pipeline was
    started before the watch stream but still after `ApplyChangePack` applied
    the attach response — the one apply in `attachDocument` — so a pack
    carrying two or more events would wedge `Attach` on the capacity-one event
    channel. Ordering fixed in one place is not an invariant until every
    publisher in the function is behind it.

## Round 4 — test adequacy

The panel accepted the delta's behavior but found its coverage one-sided: the
attach reordering, the `stopWatchPipeline` cleanups on `attachDocument`'s two
early exits, and `finishDeactivation`'s retirement were all argued for in
comments and exercised by nothing.

- `watchInitServer` now carries the `AttachDocument` response pack
  (`attachChanges`, `attachRemoved`), so a test can drive `attachDocument`'s
  `ApplyChangePack` with real remote changes instead of the empty pack the
  fixtures had answered with since the file was written. Every attach test in
  the package had been applying nothing, which is exactly why the deadlock the
  reordering fixes was invisible.
- `TestAttachStartsPumpBeforeApplyingPack` and
  `TestAttachStopsPipelineOnFailedPaths` both fail against the pre-fix ordering
  and against the cleanups removed, so they are reproducers rather than
  descriptions.
- *A post-condition test is not a branch test, and saying so is cheaper than
  pretending otherwise.* `finishDeactivation`'s retirement loop cannot be
  reached with a live pipeline today — every caller has already been through
  `Deactivate`'s own loop — so `TestCloseRetiresTheDeliveryPipeline` pins the
  state it guarantees and the comment says the loop is defensive. Writing the
  test as if it covered the branch would have left the next reader believing a
  guard was exercised when it never runs.
- *Asserting a wedge needs its own helper.* `assertNoConsumer` is the inverse of
  `assertPublishes`: a retired pipeline is only observable as a publisher that
  parks on the capacity-one channel, so the test emits two events and requires
  the second not to complete.

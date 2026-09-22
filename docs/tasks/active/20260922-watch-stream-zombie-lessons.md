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
So the fix converts silence into a visible stop, not into a recovered watch.
That is the convention `streamEvents` already set, so it is not this PR's
invention, and a closed channel beats no signal — but "the handler returns" and
"the client keeps watching" are different claims, and only the first one is
true here. The PR body says so rather than implying the stronger one.

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
- **Rounds 2 (design fit) and 3 (security / docs): not run.** This was a
  targeted verification pass, not the bounded `/self-review` loop, and a round
  that did not happen must not read as a clean one. CodeRabbit's automated
  review found exactly one issue — the same unset-oneof gap — which is
  corroboration on correctness, not a substitute for the two rounds.

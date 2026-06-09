**Created**: 2026-06-09

# Self-Prune Stale Document Subscriptions

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reclaim leaked document `Subscription[E]` entries that survive when the owning WatchDocument stream never invokes its `defer` cleanup (half-closed TCP, hung client, dropped HTTP/2 stream). Today the only path that removes a subscription from `Subscriptions.internalMap` is the stream handler's `defer s.backend.PubSub.Unsubscribe(...)`. When that trigger never fires, `BatchPublisher.publish()` keeps emitting `"Publish to %s timeout or closed"` for the dead subscriber every cycle indefinitely.

**Architecture:** Track consecutive `Publish` failures inside `Subscription[E]`. When the counter crosses a per-subscription threshold, the subscription closes its events channel and marks itself dead. `BatchPublisher.publish()` checks `IsDead()` before delivering to a subscriber, skips dead entries, and calls `Subscriptions.Delete(id)` for them after the event loop. Stream-lifecycle cleanup via the handler's `defer` remains the primary cleanup path; self-prune is a safety net for the dead-stream case.

**Tech Stack:** Go, internal `package pubsub` unit tests, `-tags integration` test

**Spec:** `docs/design/pub-sub.md` — extend the "Risks and Mitigation" section.

---

## Background

A document subscription has two end-of-life conditions today:

1. The WatchDocument streaming RPC handler exits and runs `defer
   s.backend.PubSub.Unsubscribe(ctx, docKey, sub)` (`server/rpc/yorkie_server.go`).
2. Nothing else.

When the stream context never fires `Done()` (the client's TCP went away
but the server's HTTP/2 layer did not detect it, the client crashed
mid-stream, etc.), `Subscriptions.internalMap` keeps the entry alive
forever. Every `BatchPublisher.publish()` tick then iterates over the
dead entry and emits a log line at INFO level, with no path to
reclamation.

Operations evidence (qa cluster, 2026-06-09): a deactivated client whose
DB record shows `status: deactivated, documents[doc].status: detached,
attached_docs: []` from five days ago is still being targeted by
publishes on at least one pod, producing the bulk of the daily log
volume.

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `server/backend/pubsub/subscription.go` | Add `failureCount`/`maxFailures` fields, self-prune in `Publish`, `IsDead` accessor |
| Modify | `server/backend/pubsub/batch_publisher.go` | Skip dead subs in iteration, reap via `Subscriptions.Delete` after the loop |
| Add | `server/backend/pubsub/subscription_test.go` | Internal-package unit tests for self-prune behavior |
| Modify | `server/backend/pubsub/pubsub_test.go` | External-package test asserting `Subscriptions.Len()` shrinks after dead-sub publishes |
| Add | `test/integration/subscription_leak_test.go` | Integration test: stuck WatchDocument stream is reaped within a bounded number of publish cycles |
| Modify | `docs/design/pub-sub.md` | Expand "Risks and Mitigation" with the leak description and self-prune mitigation |

---

### Task 1: Failing Internal Unit Tests

**Files:**
- Add: `server/backend/pubsub/subscription_test.go`

- [ ] **Step 1: Write the failing tests**

Create `server/backend/pubsub/subscription_test.go` in `package pubsub`
(internal) so the new fields are reachable. Three subtests:

```go
package pubsub

import (
    "testing"
    gotime "time"

    "github.com/stretchr/testify/assert"

    "github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestSubscription_SelfPrune(t *testing.T) {
    actor, err := time.ActorIDFromBytes([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
    assert.NoError(t, err)

    t.Run("publish success resets failure count", func(t *testing.T) {
        sub := NewSubscription[int](actor, 1)
        sub.maxFailures = 3

        // Fill the buffer once.
        assert.True(t, sub.Publish(1))
        // Next Publish blocks for publishTimeout then fails.
        assert.False(t, sub.Publish(2))
        // Drain to free the buffer.
        <-sub.Events()
        // Successful publish must reset the counter.
        assert.True(t, sub.Publish(3))
        assert.False(t, sub.IsDead())
    })

    t.Run("dead after consecutive failures", func(t *testing.T) {
        sub := NewSubscription[int](actor, 1)
        sub.maxFailures = 3

        assert.True(t, sub.Publish(1)) // buffer full
        for range sub.maxFailures {
            assert.False(t, sub.Publish(99))
        }
        assert.True(t, sub.IsDead())
    })

    t.Run("dead publish short-circuits without waiting timeout", func(t *testing.T) {
        sub := NewSubscription[int](actor, 1)
        sub.Close()

        start := gotime.Now()
        assert.False(t, sub.Publish(42))
        assert.Less(t, gotime.Since(start), publishTimeout/2)
    })
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./server/backend/pubsub/ -run TestSubscription_SelfPrune -v -count=1`

Expected: compile error (fields not yet declared).

---

### Task 2: Implement Self-Prune in Subscription

**Files:**
- Modify: `server/backend/pubsub/subscription.go`

- [ ] **Step 1: Add fields and constructor wiring**

Add to the constants block:

```go
// defaultMaxConsecutivePublishFailures is the threshold of consecutive
// Publish failures (timeout or already-closed channel) after which a
// Subscription marks itself dead and lets the BatchPublisher reap it.
// Set conservatively so transient slow consumers are not pruned, while
// keeping leaked subscriptions from accumulating indefinitely.
defaultMaxConsecutivePublishFailures = 100
```

Add fields to `Subscription[E]`:

```go
failureCount int
maxFailures  int
```

Initialize `maxFailures` in `NewSubscription`:

```go
maxFailures: defaultMaxConsecutivePublishFailures,
```

- [ ] **Step 2: Update Publish to count and self-close**

Modify `Subscription[E].Publish`:

```go
func (s *Subscription[E]) Publish(event E) bool {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.closed {
        return false
    }

    select {
    case s.events <- event:
        s.failureCount = 0
        return true
    case <-gotime.After(publishTimeout):
        s.failureCount++
        if s.failureCount >= s.maxFailures {
            s.closed = true
            close(s.events)
        }
        return false
    }
}
```

- [ ] **Step 3: Add IsDead accessor**

```go
// IsDead reports whether this Subscription has been closed, either
// explicitly via Close or by self-prune after too many consecutive
// Publish failures. The BatchPublisher uses this to skip and reap
// stale subscriptions.
func (s *Subscription[E]) IsDead() bool {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.closed
}
```

- [ ] **Step 4: Re-run unit tests**

Run: `go test ./server/backend/pubsub/ -run TestSubscription_SelfPrune -v -count=1`

Expected: PASS.

---

### Task 3: Reap Dead Subscriptions in BatchPublisher

**Files:**
- Modify: `server/backend/pubsub/batch_publisher.go`

- [ ] **Step 1: Skip dead subs during publish**

Replace the inner loop in `publish()` with:

```go
var deadIDs []string
for _, sub := range bp.subs.Values() {
    if sub.IsDead() {
        deadIDs = append(deadIDs, sub.ID())
        continue
    }
    for _, event := range events {
        if bp.filter != nil && bp.filter(sub.Subscriber(), event) {
            continue
        }
        if ok := sub.Publish(event); !ok {
            bp.logger.Infof(
                "Publish to %s timeout or closed",
                sub.Subscriber(),
            )
            if sub.IsDead() {
                deadIDs = append(deadIDs, sub.ID())
                break
            }
        }
    }
}
for _, id := range deadIDs {
    bp.subs.Delete(id)
}
```

- [ ] **Step 2: Confirm Close() idempotency holds**

`Subscriptions.Delete` runs `sub.Close()` inside the cmap callback.
`Subscription.Close` already guards with `if !s.closed`, so a sub that
self-pruned earlier is safe to double-close. No code change needed
here — just confirm by re-reading lines 70-78 of `subscription.go`.

---

### Task 4: External-Package Reap Test

**Files:**
- Modify: `server/backend/pubsub/pubsub_test.go`

- [ ] **Step 1: Add a subtest**

Inside the existing `TestPubSub` block, append:

```go
t.Run("stale subscription is reaped by batch publisher", func(t *testing.T) {
    pubSub := pubsub.New()
    refKey := types.DocRefKey{
        ProjectID: types.ID("000000000000000000000000"),
        DocID:     types.ID("000000000000000000000000"),
    }

    ctx := context.Background()
    sub, _, err := pubSub.Subscribe(ctx, idA, refKey, 0)
    assert.NoError(t, err)

    // Never drain sub.Events(); fill the buffer (size 1 for DocSubscription)
    // and emit publishes until the publisher reaps it.
    docEvent := events.DocEvent{Type: events.DocChanged, Actor: idB, Key: refKey}
    // Each publish() cycle attempts to send up to len(events) items; the
    // default threshold is 100 consecutive failures. Emit enough events
    // to cross that bar within a reasonable timeout.
    deadline := gotime.Now().Add(30 * gotime.Second)
    for gotime.Now().Before(deadline) {
        pubSub.Publish(ctx, idB, docEvent)
        if sub.IsDead() {
            break
        }
        gotime.Sleep(50 * gotime.Millisecond)
    }
    assert.True(t, sub.IsDead(), "subscription should be marked dead after repeated full-buffer publishes")

    // Allow one publisher cycle (window 100ms) to reap.
    gotime.Sleep(300 * gotime.Millisecond)
    assert.Equal(t, 0, len(pubSub.ClientIDs(refKey)),
        "BatchPublisher should remove the dead subscription from Subscriptions")
})
```

> Note: `Subscription.IsDead` is exported, so this external test can reference it.

- [ ] **Step 2: Run**

Run: `go test ./server/backend/pubsub/ -v -count=1`

Expected: all PASS (existing + new subtest).

---

### Task 5: Integration Test for Stuck Stream

**Files:**
- Add: `test/integration/subscription_leak_test.go`

- [ ] **Step 1: Write the test**

Use the existing helpers in `test/integration/` (see other tests for the
pattern of starting a `defaultServer` and creating clients). Set up:

- Client A: attaches doc, calls Watch, but instead of consuming stream
  events, blocks (`<-make(chan struct{})`) on the receive goroutine to
  simulate a stuck consumer that never reads.
- Client B: attaches the same doc and pushes enough updates to fill A's
  subscription buffer and cross `maxFailures` over time.
- Assertion: within `~maxFailures * publishTimeout` plus a slack
  multiplier, the server's `Subscriptions` for that doc contains only
  client B (or 0 entries) — check via an admin endpoint or by reading
  `pubSub.ClientIDs(docKey)` against the backend instance the test
  controls.

If the test harness does not expose the in-process `pubSub` directly,
fall back to asserting that the log line `Publish to %s timeout or
closed` stops emitting for client A's ActorID within the bounded
window. The first form is preferred since it asserts the structural
invariant rather than a log side-effect.

- [ ] **Step 2: Run**

Run: `go test -tags integration ./test/integration/ -run TestSubscriptionLeak -v -count=1`

Expected: PASS.

---

### Task 6: Update Design Doc

**Files:**
- Modify: `docs/design/pub-sub.md`

- [ ] **Step 1: Expand "Risks and Mitigation"**

The current text reads only: *"Currently, Subscription instances are
managed in memory."*

Replace with a fuller treatment that names two risks and their
mitigations:

1. **Subscription leak on dead streams.** The handler-`defer` cleanup
   path requires the WatchDocument stream to terminate. Half-closed TCP
   or dropped HTTP/2 streams skip that path, leaving subscriptions
   resident.
   *Mitigation:* `Subscription[E]` tracks consecutive `Publish` failures.
   After `defaultMaxConsecutivePublishFailures`, the subscription closes
   itself; `BatchPublisher.publish()` skips and removes it on the next
   cycle.
2. **In-memory only.** State is per-pod. A client whose stream lands on
   pod X cannot have its subscription reaped from pod Y. The self-prune
   mitigation runs locally on each pod where the dead subscription was
   created — sufficient for the leak case, but does not provide
   cluster-wide subscription introspection.

Also add a short paragraph below the existing "How does it work?"
section noting the prune path:

> When `Subscription.Publish` returns false repeatedly, the subscription
> marks itself dead via `IsDead`. On its next iteration, the
> `BatchPublisher` skips the entry and calls `Subscriptions.Delete`,
> returning the slot to `subscriptionsMap`.

---

### Task 7: Lint and Full Test

- [ ] **Step 1: Lint**

Run: `make lint`

Expected: no errors.

- [ ] **Step 2: Unit tests**

Run: `go test ./...`

Expected: all PASS.

- [ ] **Step 3: Integration tests (MongoDB required)**

Bring up the integration environment per `CLAUDE.md`:

```sh
docker compose -f build/docker/docker-compose.yml up --build -d
```

Run: `make test`

Expected: all PASS, including the new `TestSubscriptionLeak`.

---

### Task 8: Commit Sequence and PR

- [ ] **Step 1: Commit pieces separately**

```bash
git add server/backend/pubsub/subscription_test.go
git commit -m "Add failing self-prune tests for stale Subscription"

git add server/backend/pubsub/subscription.go
git commit -m "Mark Subscription dead after consecutive Publish failures"

git add server/backend/pubsub/batch_publisher.go server/backend/pubsub/pubsub_test.go
git commit -m "Reap dead Subscriptions from BatchPublisher iteration"

git add test/integration/subscription_leak_test.go
git commit -m "Add integration test for stuck-stream subscription cleanup"

git add docs/design/pub-sub.md
git commit -m "Document subscription leak risk and self-prune mitigation"

git add docs/tasks/active/20260609-subscription-self-prune-todo.md \
        docs/tasks/active/README.md
git commit -m "Record subscription self-prune task"
```

Each subject must be ≤ 70 chars; body lines ≤ 80; line 2 blank
(commit-msg validator enforces this).

- [ ] **Step 2: Rebase against latest main**

```bash
git fetch origin main
git rebase origin/main
```

- [ ] **Step 3: Push and open PR**

`gh` is wired to GHE in this environment, so use the GitHub REST API
directly with a `github.com`-scoped token. Title ≤ 70 chars; body =
Summary + Test plan.

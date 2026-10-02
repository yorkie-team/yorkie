---
title: watch-access-revalidation
target-version: 0.7.24
---

# Watch Access Revalidation

## Problem

`Watch`, `WatchDocument` and `WatchChannel` ask the auth webhook once, at
admission. The stream loop never asks again, so a stream admitted before a
revocation keeps delivering for as long as it stays open (#2073):

- broadcast payloads (cursors, chat, anything an app sends that way),
- presence: channel session counts, document watched/unwatched,
- DocumentChanged notifications.

Document contents are not part of it: `PushPull` asks the webhook on every
call, behind the auth webhook cache (`AuthWebhookCacheTTL`, 10s by default).
That cache is a second, smaller gap: a cached allow outlives a revocation for
up to one TTL on every RPC (#2068).

### Goals

- A revocation reaches already-open streams within one webhook round trip of
  the customer saying so, on every node.
- No webhook traffic while nothing changes.
- The customer's webhook stays the only source of truth for decisions.

### Non-Goals

- Detecting a revocation the customer does not report. Yorkie cannot see the
  customer's permission store; see Alternatives.
- Re-checking `PushPull` or other unary RPCs beyond dropping cached decisions.

## Design

Only the customer's backend knows when a permission changed, so it reports
it:

```
AdminService.RevalidateAccess({keys})          (project secret key)
  └─ Backend.BroadcastRevalidateAccess          every cluster node
       └─ ClusterService.RevalidateAccess({project_id, keys})
            1. auth.DropCachedDecisions(project, keys)
            2. watchRegistry.revalidate(project, keys, RecheckAccess)
```

### Stream registry

Each node keeps a `watchRegistry` of the Watch streams it serves: project,
token, the `AccessInfo` the stream was admitted with, and a
`context.CancelCauseFunc`. `admitWatch` registers a stream and then verifies
it, and the handler streams under the registered context. The stream loops
(`streamMergedEvents`, `streamEvents`) end with the revalidation's denial when
it closed the stream, and with `context.Canceled` otherwise, as before. They
check the context again right before each send: `select` picks among ready
cases at random, so a queued event could otherwise still reach a client after
its denial.

Registering before verifying narrows the race with a concurrent
revalidation: one that runs after registration finds the stream; one that ran
before it already dropped the cached decisions this admission reads. One
window remains: a webhook answer that was in flight when the cache was
dropped can still be cached afterwards, and a stream admitted from it after
the revalidation finished is not re-checked until the next one. A stream
already registered is safe, because a recheck never reads the cache. A
per-project cache generation would close the rest; see Risks.

### Revalidation

1. Drop the project's cached webhook decisions about the keys (all of them
   when `keys` is empty) on this node, so unary RPCs ask the webhook again
   too. Denials are cached as well, so a grant needs the same call to take
   effect before the cache TTL.
2. Select the project's streams that watch any of `keys` (all of them when
   `keys` is empty), and group them by (token, method, attributes): each group
   costs one webhook call, by a pool of 16 workers. Keys match exactly: a
   channel key does not cover its sub-paths. A call takes at most 1000 keys.
   Each check is a recheck (`auth.RecheckAccess`): it skips the cache, which
   may predate the change, and asks once without the project's retries, since
   the caller retries the whole revalidation. With the default retry policy a
   single failing check would otherwise outlast the cluster RPC timeout.
3. Only a definite answer changes a stream. Allowed: it continues untouched.
   - `ErrPermissionDenied`: close with it; the SDK does not retry.
   - `ErrUnauthenticated`: close with it; the SDK asks `authTokenInjector` for
     a new token and reconnects through admission. A stream still holding an
     expired short-lived token therefore reconnects once with a fresh one.
   - Anything else (the webhook failing, the revalidation's own context
     ending): leave the stream as it is and fail with
     `ErrRevalidationIncomplete` (Unavailable) so the caller retries. An
     uncertain answer says nothing about access; closing on it would
     disconnect users who kept access and send them all to a failing webhook
     at once.

### Cluster

`BroadcastRevalidateAccess` calls every node concurrently, not only the shard
owner of a key: a stream opened before a rebalance stays on the node it was
opened on (sharded-cluster-mode.md, split brain). If any node fails, the
admin call fails, since the revocation may not have reached that node.
Retrying is safe.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Customer never calls the API | Behavior is unchanged from today; documented contract. A per-project max stream age is a possible follow-up |
| Burst of webhook calls on a project-wide revalidation | Dedup by decision, 16 concurrent calls; callers should pass keys |
| A client that stopped reading keeps its stream until the write fails | It receives nothing after the cancel; the HTTP server reaps it |
| A node is unreachable during the broadcast | The admin call fails and can be retried |
| An in-flight webhook allow is cached after the drop | Next revalidation catches it; follow-up: per-project cache generation |
| A node fails to load the project | It neither drops its cache nor re-checks; the call fails and is retried |
| Project-wide revalidation outlasts the cluster RPC timeout | Unreached streams stay open and the call fails; callers should pass keys |
| Webhook down while revoking | Revoked streams stay open and the call keeps failing until the webhook answers; admission is equally blocked meanwhile |
| A node whose membership lease lapsed still serves streams | It is skipped like in `BroadcastCacheInvalidation`; it rejoins on its next heartbeat |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Push from the customer instead of polling the webhook | Polling trades cutoff latency against webhook load and tolerance of a slow webhook; #2075 could not settle all three |
| Revalidate instead of closing every matching stream | Closing everyone forces reconnects and watched/unwatched churn for users who kept access |
| Act only on definite answers | An outage must not turn into a mass disconnect; the caller learns the revocation is incomplete and retries |
| Recheck without cache and retries | The cache may predate the change; retries belong to the caller, inside one RPC deadline they only time out |
| Drop the cached decisions about the keys | Fixes the unary cache gap for the revoked access without sending the whole project back to the webhook |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Per-stream lease that polls the webhook (#2075) | Webhook load grows with open streams; any cutoff shorter than the webhook's own timeout kills healthy streams |
| Close streams on a fixed max age | Bounded but slow cutoff and periodic reconnects for everyone; kept as an optional follow-up |
| Token expiry carried in the webhook response | Needs an API change in every customer's webhook and still cannot express "revoked now" |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

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
            1. auth.DropCachedDecisions(project)
            2. watchRegistry.revalidate(project, keys, VerifyAccessAs)
```

### Stream registry

Each node keeps a `watchRegistry` of the Watch streams it serves: project,
token, the `AccessInfo` the stream was admitted with, and a
`context.CancelCauseFunc`. `admitWatch` registers a stream and then verifies
it, and the handler streams under the registered context. The stream loops
(`streamMergedEvents`, `streamEvents`) return `context.Cause(ctx)`, so a stream
closed by a revalidation ends with the reason the registry gave.

Registering before verifying narrows the race with a concurrent
revalidation: one that runs after registration finds the stream; one that ran
before it already dropped the cached decisions this admission reads. One
window remains: a webhook answer that was in flight when the cache was
dropped can still be cached afterwards, and a stream admitted from it is not
re-checked until the next revalidation. A per-project cache generation would
close it; see Risks.

### Revalidation

1. Drop every cached webhook decision of the project on this node, so unary
   RPCs ask the webhook again too.
2. Select the project's streams that watch any of `keys` (all of them when
   `keys` is empty), and group them by (token, method, attributes): each group
   costs one webhook call, at most 16 at a time. Keys match exactly: a channel
   key does not cover its sub-paths. A call takes at most 1000 keys.
3. Allowed: the stream continues untouched.
   - `ErrPermissionDenied`: close with it; the SDK does not retry.
   - `ErrUnauthenticated`: close with it; the SDK asks `authTokenInjector` for
     a new token and reconnects through admission. A stream still holding an
     expired short-lived token therefore reconnects once with a fresh one.
   - Webhook failure: close with `ErrRevalidationUnavailable` (Unavailable).
     The SDK reconnects and the new stream goes through admission, so the
     server needs no policy of its own for an uncertain answer.
   - The revalidation's own context ending (caller gone, cluster RPC timeout):
     close nothing, since it says nothing about access, and fail the node RPC
     so the caller retries. Closing here would disconnect users who kept
     access and send them all to an already slow webhook at once.

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

### Design Decisions

| Decision | Reason |
|----------|--------|
| Push from the customer instead of polling the webhook | Polling trades cutoff latency against webhook load and tolerance of a slow webhook; #2075 could not settle all three |
| Revalidate instead of closing every matching stream | Closing everyone forces reconnects and watched/unwatched churn for users who kept access |
| Unavailable on webhook failure | Lets admission, which already handles this case, decide on reconnect |
| Drop the project's cached decisions | One call fixes both the stream gap and the unary cache gap for the revoked access |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Per-stream lease that polls the webhook (#2075) | Webhook load grows with open streams; any cutoff shorter than the webhook's own timeout kills healthy streams |
| Close streams on a fixed max age | Bounded but slow cutoff and periodic reconnects for everyone; kept as an optional follow-up |
| Token expiry carried in the webhook response | Needs an API change in every customer's webhook and still cannot express "revoked now" |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

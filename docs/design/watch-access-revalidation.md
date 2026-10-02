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
up to one TTL on every RPC (#2068). A server that cannot accept that gap sets
`AuthWebhookCacheDisabled` (`--auth-webhook-cache-disabled`): it neither reads
nor writes the cache, so every protected RPC asks the webhook, and a
revalidation has no cached decisions to drop.

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

Registering before verifying closes the race with a concurrent revalidation:
one that runs after registration finds the stream; one that ran before it
already dropped the cached decisions this admission reads. The remaining
window — a webhook answer in flight when the cache was dropped being cached
after the drop, admitting a stream the finished revalidation never saw — is
closed by a cache generation (`auth.cacheGen`): a verification reads the
generation before asking the webhook and writes the answer back only while it
still matches, under a lock the drop takes exclusively. An answer that
crossed a drop is therefore discarded, and the next access asks the webhook
itself. A stream already registered is safe anyway, because a recheck never
reads the cache.

### Revalidation

1. Drop the project's cached webhook decisions about the keys (all of them
   when `keys` is empty) on this node, so unary RPCs ask the webhook again
   too. Denials are cached as well, so a grant needs the same call to take
   effect before the cache TTL. With `AuthWebhookCacheDisabled` this step
   does nothing.
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
   - An answer that does not conform — 200 with `allowed=false`, an
     unexpected status, a body that does not parse
     (`ErrInvalidJSONResponse`, `ErrUnexpectedStatusCode`): close with it.
     Admission rejects a Watch on exactly these, and a revalidation that
     called them uncertain would let a webhook keep a revoked stream open
     forever by answering in a shape neither side accepts. Closing is the
     recoverable side: the client reconnects and admission asks again under
     the project's full retry policy.
   - Having no answer at all (the webhook unreachable or timing out, the
     revalidation's own context ending): leave the stream as it is and fail
     with `ErrRevalidationIncomplete` (Unavailable) so the caller retries.
     A missing answer says nothing about access; closing on it would
     disconnect users who kept access and send them all to a failing webhook
     at once.
4. Webhook calls stop `revalidateReplyMargin` (500ms) before the caller's
   deadline, so a node with more distinct accesses than the deadline allows
   answers with what it verified instead of dying on the deadline. The
   unverified groups are reported as `ErrRevalidationIncomplete`.

### Cluster

`BroadcastRevalidateAccess` calls every node concurrently, not only the shard
owner of a key: a stream opened before a rebalance stays on the node it was
opened on (sharded-cluster-mode.md, split brain). If any node fails, the
admin call fails, since the revocation may not have reached that node.
Retrying is safe.

The call gets `revalidateRPCTimeout` (30s), not the generic cluster RPC
timeout (10s): a node's work grows with the distinct accesses it serves,
while the generic timeout is sized for a single lookup. The cluster HTTP
client's hard limit (`ClusterClientTimeout`) still caps it, so the deadline
asked for is never longer than the request can live.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Customer never calls the API | Behavior is unchanged from today; documented contract. A per-project max stream age is a possible follow-up |
| Burst of webhook calls on a project-wide revalidation | Dedup by decision, 16 concurrent calls; callers should pass keys |
| A client that stopped reading keeps its stream until the write fails | It receives nothing after the cancel; the HTTP server reaps it |
| A node is unreachable during the broadcast | The admin call fails and can be retried |
| An in-flight webhook allow is cached after the drop | The cache generation discards it, so the next access asks the webhook |
| A node fails to load the project | It neither drops its cache nor re-checks; the call fails and is retried |
| Project-wide revalidation outlasts its RPC timeout | 30s rather than 10s, and the node stops in time to report what it verified as `ErrRevalidationIncomplete`; callers should pass keys and retry |
| Webhook down while revoking | Revoked streams stay open and the call keeps failing until the webhook answers; admission is equally blocked meanwhile |
| A node whose membership lease lapsed still serves streams | It is skipped like in `BroadcastCacheInvalidation`; it rejoins on its next heartbeat |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Push from the customer instead of polling the webhook | Polling trades cutoff latency against webhook load and tolerance of a slow webhook; #2075 could not settle all three |
| Revalidate instead of closing every matching stream | Closing everyone forces reconnects and watched/unwatched churn for users who kept access |
| Act only on definite answers | An outage must not turn into a mass disconnect; the caller learns the revocation is incomplete and retries |
| Treat a non-conforming answer as a denial | Admission already rejects on it; the asymmetry would otherwise make a revocation unenforceable against such a webhook |
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

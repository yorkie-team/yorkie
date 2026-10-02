**Created**: 2026-10-02

# Revalidate open Watch streams on demand

**Issue:** yorkie-team/yorkie#2073. Supersedes the polling approach of #2075.
Related: #2068 (cached allows outlive a revocation).

## Problem

`Watch`, `WatchDocument` and `WatchChannel` call `auth.VerifyAccess` once at
admission. `streamMergedEvents` and `streamEvents` never look at authorization
again, so a stream admitted before a revocation keeps delivering broadcast
payloads, presence counts, watched/unwatched and DocumentChanged events for as
long as it stays open.

Reproduced on `f44ce2fa`: alice is revoked, 11s later her new Broadcast and
Watch are denied, yet bob's broadcast payload still reaches her old stream.
Document contents are not affected: PushPull re-verifies on every call (behind
the 10s auth cache).

## Approach

Revocation is a fact only the customer's backend knows, so it pushes it:

```
AdminService.RevalidateAccess({keys})        // project secret key
  -> every node: ClusterService.RevalidateAccess({project_id, keys})
       1. drop the project's auth webhook cache entries
       2. re-verify each local Watch stream of the project that covers any
          of the keys (all of them when keys is empty), one webhook call per
          distinct (token, method, attributes)
       3. denied           -> close the stream with the webhook's error
          webhook failure  -> close with Unavailable; the SDK reconnects and
                              goes through admission again
```

Broadcast to every node, not unicast by shard key: a stream opened before a
rebalance stays on its old node (sharded-cluster-mode.md, split brain).

## Plan

- [x] Proto: `AdminService.RevalidateAccess`, `ClusterService.RevalidateAccess`;
      `make proto`
- [x] `pkg/cache`: remove entries by predicate on `LRUWithExpires`
- [x] `server/rpc/auth`: verify for an explicit project/token
      (`VerifyAccessAs`) and drop a project's cached decisions
- [x] `server/rpc`: `watchRegistry` — register the three Watch handlers with a
      cancel-cause context; stream loops return `context.Cause(ctx)`
- [x] `clusterServer.RevalidateAccess` -> registry; `adminServer.RevalidateAccess`
      -> `Backend.BroadcastRevalidateAccess` -> cluster client
- [x] `admin.Client.RevalidateAccess` for tests and tooling
- [x] Tests
  - [x] registry unit tests: key filtering, dedup, deny/allow/error outcomes
  - [x] integration (memory backend): revoked stream closes with
        PermissionDenied right after the call; allowed and unrelated streams
        stay open; legacy WatchDocument/WatchChannel covered
- [x] Design doc `docs/design/watch-access-revalidation.md` + README entry
- [x] `make verify`

## Out of scope (follow-ups)

- JS SDK: surface `auth-error` on a Watch closed with PermissionDenied
  (today only Unauthenticated publishes it).
- Optional per-project max stream age as a safety net for customers that
  cannot call the API.
- A stream blocked in `Send` on a client that stopped reading is not cut
  until the write fails; it also receives nothing new.

## Review

- `make verify` green; `make test` (MongoDB) green; new tests pass 3/3 under
  `-race`.
- Self review round 1 (correctness/tests), one blocking finding, fixed:
  a revalidation whose own context ended (cluster RPC timeout, caller gone)
  closed every not-yet-verified stream as Unavailable. It now closes nothing
  it did not verify and fails so the caller retries. Also applied: concurrent
  node fan-out, a 1000-key cap and a key set, exact-match semantics
  documented, closed count excludes streams already ended, plain
  cancellations and client deadlines still end streams as Canceled,
  WatchDocument covered end to end.
- Kept as known limitations (design doc, Risks): an in-flight allow cached
  after the drop; a node that cannot load the project skips its cache drop;
  Unauthenticated on an expired admission token makes the SDK reconnect once.

### Code review round 2 (`/code-review high` + #2075 cross-check)

Cross-checked the 48 distinct blocking findings of #2075's five panel
rounds. Polling, lease and write-deadline findings do not apply; enabling or
disabling the webhook mid-stream behaves correctly (now a regression test).
Fixed what did apply or was new:

- [x] A queued event could still be sent after a denial (`select` picks at
      random): measured 1002 sends over 1000 runs; now checked before each
      send. Red/green verified.
- [x] A recheck could read an allow re-cached after the drop: rechecks skip
      the cache.
- [x] With default retries one failing check outlasts the 10s cluster RPC
      timeout: rechecks ask once.
- [x] Webhook failure closed every covered stream: now only a definite answer
      changes a stream; anything else fails the call with
      `ErrRevalidationIncomplete`.
- [x] Cache drop scoped to the keys; worker pool; streams closed before
      admission not counted.
- Kept: exact key match, expired admission token reconnects once,
  lapsed-membership node skipped (design doc, Risks).

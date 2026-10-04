# Lessons: integration flakes after the server-side size gate

**Created**: 2026-10-04

- `-count=N` failures and CI failures had different causes. The `-count`
  ones (key reuse) were easy to reproduce and would have hidden the CI one
  (a cache race) if fixed alone. Read the CI log's error, not just the test
  name.
- A flake that appears with an unrelated change can be load, not logic. The
  CI log had `failed to compact ... deadline exceeded` and a one-minute
  `CompactDocument` RPC next to the failure; that pointed at the leftover
  18MB document.
- "Write to MongoDB, then put the returned row in the cache" is two steps.
  Any cache filled that way needs the write and the fill under one lock per
  key, or a version to compare. A bulk read cannot take those locks, so it
  should not fill the cache.
- A stress test at the database layer (three goroutines on one client row)
  reproduced the race 5 of 5 times in under a second, where the full
  integration path needed hundreds of iterations.
- A wait on a background snapshot is not a timeout problem when the server
  may decide not to take the snapshot. Wait for the condition that makes
  the assertion meaningful (a snapshot at the current server seq) and drive
  it.

## Self review

- Round 1 (correctness/tests): no blocking findings. One should-fix: the
  first `waitForSnapshot` nudged on every poll where the snapshot lagged,
  and each nudge moves the server seq, so a snapshot slower than the poll
  interval could never catch up. Fixed: fix the target before waiting and
  nudge only when no snapshot landed since the last poll; the loop also no
  longer calls `require` inside `assert.Eventually`'s goroutine. Also fixed:
  `TestKey` dropped the run suffix for names cut at 100 characters. Kept as
  known limitations: the client lock is held across one MongoDB round trip
  and ignores ctx; several server nodes still keep separate client caches,
  as before.
- Later review rounds (panel and loop fixers) found real gaps in how a
  cached `ClientInfo` authorizes requests across nodes, and each fix
  exposed the next one: an expiring cache, conditional write-backs, an
  uncached read before a push writes, then Watch/Broadcast checks, then
  the `ClusterService.Broadcast` bypass. Eight rounds later the PR was
  changing the server's authorization model. The maintainer re-scoped it
  back to the flakes and the per-client cache race, reverted the rest, and
  filed yorkie-team/yorkie#2113 with the evidence.
- When review findings keep growing past the stated scope, especially into
  behavior that existed before the PR, stop and ask for a scope decision
  instead of fixing one more round. File an issue with the reproduction.
- Deleting a cache fill removes a refresh path as well as a bug. Here it was
  the only path that refreshed a client written by another node; that gap
  is part of #2113.
- The striped lock became a per-client `pkg/locker` lock: same guarantee,
  no false sharing between unrelated clients, and the locking utility the
  server already uses.
- The panel re-raised the gap the revert left: with the bulk-read fill gone
  and no TTL, nothing corrected a cached client row across nodes, and the
  authorization gates read that row. Rather than re-add the TTL the
  maintainer reverted, the fix uses the mechanism the repo already has —
  the cluster `InvalidateCache` RPC — and implements its `CacheTypeClient`
  case, which was the one branch of `mongo.Client.InvalidateCache` left
  unwritten. Deactivation and detach now broadcast it. An invalidation is
  a drop, so, unlike the fill that started all of this, it can never put an
  older copy of a row on top of a newer one.
- `ClientRefKey.String()` is a human-readable form, not a wire form.
  Naming a client in a cluster RPC needs one that round-trips, hence
  `CacheKey`/`ParseClientRefKey`.
- The revision RPCs confirmed the client was activated but never that it
  held the document attached, and `auth.VerifyAccess` is a no-op without a
  project auth webhook. `RestoreRevision` therefore overwrote a whole
  document for any activated client in the project. These are not per-sync
  calls, so they can afford to confirm the row in MongoDB.
- The panel round after that found the gate was applied to three of four
  revision RPCs: `CreateRevision` reads `client_id` off the wire but never
  used it, and its response carries the snapshot it builds, so the one RPC
  left ungated still handed a whole document to any caller in the project.
  A gate added to siblings reads as covering the family; it only covers
  the calls it is written into.
- A drop that cannot put an older copy on top of a newer one still has to
  take the lock the fill holds. The fill reads MongoDB and `Add`s under the
  client's lock, so a `Remove` racing it lands between the read and the
  `Add`, and the pre-write copy goes straight back in — with no TTL, for
  good. Locking the drop orders it either before the read or after the
  `Add`; both are correct.
- Invalidation belongs where the write happens. The cluster-routed detach
  handler wrote the client row and returned without broadcasting, leaving
  it to its caller — so any other caller of that RPC, or a caller that
  loses the response, left every peer's row stale.
- ...and the round after corrected that: "where the write happens" is not
  the same as "per write". `clients.Deactivate` detaches every attached
  document through the cluster handler and then drops the row itself, so
  broadcasting in both places turned one deactivation into
  (documents+1) cluster-wide fan-outs — times `DeactivateConcurrency`
  under housekeeping. One deferred broadcast in `Deactivate` covers the
  detaches and the deactivation write together, because they all land in
  the same row and a drop drops all of them.
- `defer` is the right shape for an invalidation, not a call at the end of
  the happy path. The row MongoDB holds has already moved on by the time
  the write returns, so a failure in a later step — serializing the
  response, a detach midway through a deactivation — must not be what
  decides whether the peers hear about it.
- Removing the bulk-read cache fill removed the only thing that refreshed
  a peer's row after an attach, and no attach path broadcast. Detach and
  deactivation had counterparts; attach did not, which is exactly the
  asymmetry that leaves a client the database says is attached rejected by
  every node but the one that wrote it.

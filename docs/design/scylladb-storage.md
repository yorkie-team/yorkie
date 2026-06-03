---
title: scylladb-storage
target-version: 0.7.x (proof of concept)
---

# ScyllaDB Storage (Gradual Adoption)

## Problem

Yorkie's database layer is currently MongoDB-only. The hot per-document and
per-client paths — `changes`, `snapshots`, `versionvectors`, and the
`clients` / `client_documents` / `doc_clients` family — dominate write
volume in production. We want to evaluate whether moving those write-heavy
tables to ScyllaDB (wide-column, shard-per-core) improves throughput and
latency, without rewriting the rest of the database layer.

The PoC must:

- Allow **per-table opt-in** so that one or a few write-heavy tables can
  be moved at a time, leaving the rest on MongoDB. This is essential for
  staged rollout and A/B perf testing.
- Keep the `database.Database` interface, so the rest of the server is
  unaware of which store owns which row.
- Be reversible: flipping a flag back returns ownership to MongoDB.

### Goals

- Implement a `scylla.Client` that wraps a `mongo.Client` and dispatches
  each method to ScyllaDB or MongoDB based on a per-group toggle.
- Provide a single connection-time configuration (`scylla.Config.Tables`)
  for picking which table groups live on ScyllaDB.
- Define table groups whose writes are tightly coupled, so a single
  toggle is enough.
- Make perf testing fair: provide a multi-node ScyllaDB cluster profile
  that mirrors a sharded MongoDB cluster.

### Non-Goals

- Native ScyllaDB ownership of low-write tables (`users`, `projects`,
  `members`, `invites`, `documents`, `schemas`, `revisions`,
  `clusternodes`). Those stay on MongoDB indefinitely.
- A migration tool that backfills existing MongoDB rows into ScyllaDB.
  This PoC starts with empty ScyllaDB tables; existing deployments would
  need a separate migration step.
- Cross-store transactionality. The PoC accepts that a crash mid-write
  can leave the MongoDB-side `DocInfo` and the ScyllaDB-side `changes`
  out of sync — see *Risks*.

## Design

### Table groups

ScyllaDB ownership is gated at the **group** level. Tables that move
together are in the same group:

| Group            | ScyllaDB tables                                | Methods routed                                                                                                                                                            |
|------------------|------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Clients`        | `clients`, `client_documents`, `doc_clients`  | `ActivateClient`, `TryAttaching`, `DeactivateClient`, `FindClientInfoByRefKey`, `UpdateClientInfoAfterPushPull`, `FindAttachedClientInfosByRefKey`, `FindActiveClients`, `FindAttachedClientCountsByDocIDs`, `CountActivatedClients`, `IsDocumentAttachedOrAttaching` |
| `Changes`        | `changes`                                      | `CreateChangeInfos`, `CompactChangeInfos`, `FindLatestChangeInfoByActor`, `FindChangesBetweenServerSeqs`, `FindChangeInfosBetweenServerSeqs`                              |
| `Snapshots`      | `snapshots`                                    | `CreateSnapshotInfo`, `FindSnapshotInfo`, `FindClosestSnapshotInfo`                                                                                                       |
| `VersionVectors` | `versionvectors`                               | `UpdateMinVersionVector`, `GetMinVersionVector`                                                                                                                           |

Every other database method always runs against MongoDB.

### Dispatch

`scylla.Client` holds a `Tables` struct copied from `scylla.Config.Tables`:

```go
type Tables struct {
    Clients        bool
    Changes        bool
    Snapshots      bool
    VersionVectors bool
}
```

Each scylla-native method begins with a dispatch check:

```go
func (c *Client) CreateChangeInfos(...) (..., error) {
    if !c.tables.Changes {
        return c.mongo.CreateChangeInfos(ctx, refKey, checkpoint, changes, isRemoved)
    }
    // ScyllaDB path: writes to scylla `changes` table, then updates the
    // MongoDB-side DocInfo via the mongo.UpdateDocAfterChanges helper.
}
```

When a group is disabled the call delegates to the underlying
`mongo.Client`; mongo's own caches and logic handle it. When a group is
enabled the scylla path runs, and any cross-store coordination (e.g.,
the `DocInfo.server_seq` update after writing changes) is done via the
small set of exported helpers on `mongo.Client` (see
`server/backend/database/mongo/client_scylla_helpers.go`).

### Schema creation

`schema.go` tags every `TableDef` with its group. `createTables` skips
any table whose group is disabled, so a partial-adoption deployment
does not provision unused tables in the keyspace.

### PurgeDocument

`PurgeDocument` is the only method that touches all four groups in one
call. It now does both sides unconditionally:

```go
counts, _ := c.mongo.PurgeDocument(ctx, refKey)        // mongo per-doc tables + DocInfo row
scyllaCounts, _ := c.purgeDocumentInternals(ctx, ...)  // scylla per-doc tables (gated by toggles)
```

`DeleteMany` on an empty MongoDB collection is a no-op, so this is
correct whether changes/snapshots/VV live on MongoDB, ScyllaDB, or a
mix. The merged `counts` map is reported back to the housekeeping path.

### CompactChangeInfos

Compaction first purges the existing per-doc data and then inserts the
single compacted change. With hybrid storage the existing data may live
in either store, so the scylla impl purges both:

```go
c.mongo.PurgeDocumentInternals(...)  // mongo-side changes/snapshots/VV
c.purgeDocumentInternals(...)        // scylla-side enabled groups
```

`mongo.PurgeDocumentInternals` is a new exported wrapper around the
existing private helper; it does not touch the `documents` row.

### MongoDB-side helpers

`mongo/client_scylla_helpers.go` exposes a small surface that the scylla
client needs but the public `database.Database` interface does not:

- `UpdateDocAfterChanges(refKey, initialServerSeq, newServerSeq, hasOperations, isRemoved)` —
  the `DocInfo.server_seq` CAS that previously lived inline at the end
  of `mongo.CreateChangeInfos`. Required because changes-on-scylla
  cannot reuse `mongo.CreateChangeInfos` end-to-end.
- `CompactDocAfterChanges(projectID, docID, lastServerSeq, newServerSeq)` —
  the equivalent tail of `mongo.CompactChangeInfos`.
- `DeleteDocumentRecord(docRefKey)` — single-row delete of the `documents`
  row. Currently unused by `PurgeDocument` (which uses
  `mongo.PurgeDocument` instead) but kept as a thin helper in case other
  scylla paths need it.
- `PurgeDocumentInternals(projectID, docID)` — exported view onto the
  private mongo helper for compaction-scope purging.

All four helpers evict `mongo.docCache` before/after their write so the
next `FindDocInfoByRefKey` reads the post-update row. Without this the
scylla layer (which mutates only a deep copy of the cached `DocInfo`)
would keep filtering on a stale `server_seq` and hit
`ErrConflictOnUpdate` on every second call.

### Configuration surface

Server flags (added in `cmd/yorkie/server.go`):

```
--use-scylla-db                            Turn on scylla.Dial. Requires --mongo-connection-uri.
--scylla-hosts=127.0.0.1                   Comma-separated list of scylla hosts.
--scylla-port=9042
--scylla-keyspace=yorkie
--scylla-username=                         Empty disables password auth.
--scylla-password=
--scylla-consistency=LOCAL_QUORUM
--scylla-connect-timeout=5s
--scylla-query-timeout=3s
--scylla-monitoring-enabled
--scylla-monitoring-slow-query-threshold=200ms
--scylla-replication-factor=1              Set equal to cluster size.
--scylla-table-clients=true                Per-group toggles.
--scylla-table-changes=true
--scylla-table-snapshots=true
--scylla-table-version-vectors=true
```

The wiring sits in `server/backend/backend.go`:

```go
switch {
case scyllaConf != nil && mongoConf != nil:
    db, err = scylla.Dial(scyllaConf, mongoConf)
case mongoConf != nil:
    db, err = mongo.Dial(mongoConf)
default:
    db, err = memdb.New()
}
```

ScyllaDB without MongoDB is not a supported combination — the scylla
client is a delegating wrapper, not a standalone store.

## Perf testing: matching cluster size

An early run of the PoC against a sharded production-style MongoDB
cluster showed **no measurable difference** vs the all-MongoDB baseline.
The most likely cause is that the comparison was unfair: MongoDB was
horizontally sharded across multiple nodes, while ScyllaDB was running
as a single-node container with a `SimpleStrategy` keyspace at RF=3 (an
incoherent setting on a one-node cluster — Scylla silently downgrades
to RF=1 since it has nowhere to place the other replicas).

For a fair comparison, the ScyllaDB cluster size and replication factor
must match the MongoDB sharded cluster. Two concrete steps:

1. Bring up the `scylla-perf` profile in `build/docker/docker-compose.yml`,
   which provisions a three-node ScyllaDB cluster on the same host using
   `--seeds=` to chain them into one logical cluster:

   ```sh
   docker compose --profile scylla-perf up -d
   ```

   The first node exposes CQL on host port `9043` (the test profile keeps
   `9042` for the single-node `scylla` service).

2. Start the server with `--scylla-replication-factor` set equal to the
   cluster size:

   ```sh
   yorkie server \
       --mongo-connection-uri=mongodb://... \
       --use-scylla-db \
       --scylla-hosts=127.0.0.1 \
       --scylla-port=9043 \
       --scylla-replication-factor=3 \
       --scylla-consistency=LOCAL_QUORUM
   ```

   `ReplicationFactor` is consumed by `scylla.CreateKeySpace` when the
   keyspace is (re)created on Dial. For production-style deployments,
   prefer `NetworkTopologyStrategy` with per-DC replication factors;
   this PoC sticks to `SimpleStrategy` for simplicity (see *Risks*).

When the cluster size and RF are matched, partitions are spread across
all nodes by `partition_key = (project_id, doc_id)` (or
`(project_id, client_id)` for the clients group), which is the same
shard-key model a typical sharded MongoDB deployment uses for these
collections. Without that, ScyllaDB's load is pinned to one node and
the comparison reduces to "single-node ScyllaDB vs sharded MongoDB" —
expected to be a wash or worse.

### Tunable knobs that often matter for perf

- `--smp` per node should equal the available physical cores; the
  docker-compose perf profile uses `--smp 2` per node to fit a
  developer laptop, which is too small for representative numbers.
- `--scylla-consistency=LOCAL_QUORUM` matches a typical MongoDB
  majority-write configuration. Lower it to `ONE` only if MongoDB is
  running with `w:1`.
- `--scylla-query-timeout` defaults to 3s; perf tests that intentionally
  saturate the cluster should raise this so timeouts don't masquerade
  as latency wins.

## Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Cross-store crash leaves `DocInfo.server_seq` (MongoDB) ahead of `changes` (ScyllaDB) | PoC accepts this. `mongo.UpdateDocAfterChanges` runs **after** the scylla insert, so a crash between the two leaves rows behind on scylla rather than ahead. Recovery would require an idempotent re-push from the client (already a property of Yorkie). |
| `docCache` staleness causes `ErrConflictOnUpdate` on every other call | All scylla helpers on `mongo.Client` evict `docCache` before mutating. |
| `SimpleStrategy` is misleading on multi-DC clusters | Document switching to `NetworkTopologyStrategy` for production. PoC keeps `SimpleStrategy` for parity with the existing test setup. |
| `ALLOW FILTERING` on `FindActiveClients` / `CountActivatedClients` is a cross-partition scan | Acceptable on PoC; production would need a status-indexed materialized view or a dedicated table. |
| Misordered table groups (e.g., `Changes=true` but `VersionVectors=false`) lead to half-purged state during compaction | `CompactChangeInfos` purges both stores via `mongo.PurgeDocumentInternals` + `scylla.purgeDocumentInternals`, so this is correct for any combination. |
| Keyspace name case mismatch — Scylla folds unquoted identifiers to lowercase | `RandomKeyspace` returns lowercase-only. |

## Design Decisions

| Decision | Reason |
|----------|--------|
| Per-group toggles, not per-method | Methods within a group share underlying tables and write paths; toggling them independently is incoherent (e.g., "changes on scylla but FindChanges on mongo" reads from an empty mongo collection). |
| Delegating wrapper (scylla wraps mongo) instead of a sibling implementation | The non-ScyllaDB tables — users, projects, members, invites, docs, schemas, revisions, cluster nodes — are not going anywhere; reimplementing them is pure cost. A wrapper is also reversible by flipping flags. |
| Helpers on `mongo.Client` instead of duplicating BSON logic in scylla | Keeps the DocInfo CAS in one place; the scylla layer never speaks BSON. |
| Always-purge-both-sides in `PurgeDocument` and `CompactChangeInfos` | Idempotent: deletes on empty collections are no-ops. Avoids the combinatorial explosion of "purge from store X only if group is enabled" branches. |
| `ReplicationFactor` as a config knob, not hard-coded RF=3 | The original hard-coded RF=3 was incoherent on single-node test instances. A configurable factor lets the test setup use RF=1 while perf runs use RF=cluster_size. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Full reimplementation of `database.Database` in scylla | Most collections (users, projects, members, ...) are low-write metadata; rebuilding them buys nothing and doubles the surface area to test. |
| Single `UseScyllaDB bool` toggle (all or nothing) | Cannot stage rollout, cannot run "scylla for changes only" perf experiments. The user asked specifically for the gradual mode. |
| Move tables one method at a time (not by group) | Methods within a group share write paths and partition keys; partial enablement creates incoherent reads (e.g., `CreateChangeInfos` writes to scylla but `FindChangeInfosBetweenServerSeqs` reads from mongo). |
| Mongo-side helpers as part of `database.Database` interface | The helpers are leaks specifically for the scylla path; cluttering the public interface would oblige all backends (including memdb) to implement them. |
| Keep RF=3 on the single-node test container | Silently downgraded by Scylla; misleading and the actual reason an early perf comparison showed no difference. |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

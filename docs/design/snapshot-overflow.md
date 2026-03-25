# Snapshot Overflow

## Summary

MongoDB has a 16MB BSON document size limit. When a Yorkie document's snapshot
exceeds this limit, `CreateSnapshotInfo` fails, causing a cascading failure:
no new snapshots are created, changes accumulate, and housekeeping compaction
enters an infinite retry loop.

## Solution

Two-phase approach, both applied at the **database storage layer** (not the
converter layer) to avoid circular import issues.

### Phase 1: Snapshot Compression

Apply zstd compression to snapshot bytes in `CreateSnapshotInfo` (after
`converter.SnapshotToBytes`) and decompress in `FindSnapshotInfo` /
`FindClosestSnapshotInfo` (before returning `SnapshotInfo.Snapshot`).

A 1-byte format header distinguishes compressed from uncompressed data for
backward compatibility:

- No header (first byte != `0x01`): Raw protobuf (existing format)
- `0x01` prefix: zstd-compressed protobuf

This is safe because existing protobuf snapshots start with field tag bytes
(e.g., `0x0A` for field 1, wire type 2), never `0x01`.

Protobuf-serialized CRDT data compresses well (typically 3-5x), so most
snapshots that would exceed 16MB will fit after compression.

### Phase 2: Snapshot Body Separation

For snapshots that still exceed 12MB after compression, store the snapshot
bytes in a separate `snapshot_bodies` collection. The `snapshots` collection
stores only metadata with a `has_external_body` flag.

`snapshot_bodies` uses `doc_id` as its shard key, consistent with the existing
sharding strategy documented in `docs/design/mongodb-sharding.md`.

### Additional: Error Handling

- Log snapshot creation failures with document ID and snapshot size

## Schema

### snapshots collection (existing, modified)

New field `has_external_body` (bool): when true, snapshot bytes are in
`snapshot_bodies` instead of inline. Defaults to false for backward compat.

### snapshot_bodies collection (new)

Unique index: `(doc_id, project_id, server_seq)` — same pattern as `snapshots`.
Shard key: `doc_id` (hashed).

Fields: `_id`, `doc_id`, `project_id`, `server_seq`, `snapshot` (Binary),
`created_at` (Date).

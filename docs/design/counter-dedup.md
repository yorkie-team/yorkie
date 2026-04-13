---
title: counter-dedup
target-version: 0.7.5
---

# Counter Dedup Mode

## Problem

Yorkie's Counter works well for simple cumulative metrics like PV (Page View),
but it cannot deduplicate repeated contributions from the same user. UV (Unique
Visitor) measurement — one of the most fundamental web analytics metrics —
requires ignoring duplicate visits from the same user within a time window.
Adding dedup support to Counter lets Yorkie apps measure UV without external
analytics infrastructure.

### Goals

- Add a dedup mode to Counter that ignores duplicate `increase` calls from the
  same actor
- Use HyperLogLog internally for memory-efficient approximate cardinality
  (~16KB per Counter, ~2% error)
- Keep the existing normal-mode Counter completely unchanged
- Properly serialize/deserialize HLL state in snapshots so dedup Counters
  survive server restarts and snapshot compaction
- Support large-scale concurrent access (tens to hundreds of thousands)

### Non-Goals

- Exact UV counting (approximate counts are acceptable)
- Sharding, batching, and other large-scale optimizations (separate follow-up)
- Automated daily Document lifecycle management (client-managed)
- Undo/redo for dedup Counter — HyperLogLog is an append-only sketch with no
  remove operation, so once an actor is added it cannot be rolled back. Dedup
  Counter increases produce no reverse operation.

## Design

### Overall Structure

Create a per-page daily Document and use Counter's dedup mode for PV/UV
measurement together.

```
Document: "analytics-{pageId}-{YYYYMMDD}"
  ├── pv: Counter(IntegerType)          // normal mode — +1 per visit
  └── uv: Counter(IntegerType, dedup)   // dedup mode — ignores same actor
```

Client usage:

```js
const doc = new yorkie.Document(`analytics-home-20260413`);
await client.attach(doc);

// Initialize counters (once, on document creation)
doc.update((root) => {
  root.pv = new yorkie.Counter(yorkie.IntType, 0);                    // normal mode
  root.uv = new yorkie.Counter(yorkie.IntType, 0, { dedup: true });   // dedup mode
});

// Record a visit
doc.update((root) => {
  root.pv.increase(1);                        // PV: always increases
  root.uv.increase(1, { actor: userId });     // UV: ignored if same userId
});
```

### Counter Internal Structure

Add an optional dedup mode to Counter. When dedup is enabled, the Counter
maintains an internal HyperLogLog.

```
Counter
  ├── valueType: CounterType (Int, Long)
  ├── value: number | Long
  ├── isDedup: bool
  └── [dedup mode only]
      └── hll: HyperLogLog (precision=14, ~16KB)
```

Behavior:

| Mode | On `increase(v)` |
|------|-------------------|
| Normal | value += v (unchanged from current behavior) |
| Dedup | Add actor hash to HLL. If already seen, ignore. If new, value = hll.count() |

In dedup mode, `value` is a derived value from `hll.count()`, so it is
recomputed as `value = hll.count()` after each operation is applied.

### IncreaseOperation Extension

```
IncreaseOperation
  ├── parentCreatedAt: TimeTicket   // existing
  ├── value: Primitive              // existing
  ├── executedAt: TimeTicket        // existing
  └── actor: string (optional)      // dedup mode only
```

- When `actor` is absent: normal mode (full backward compatibility)
- When `actor` is present: dedup mode behavior

### CRDT Conflict Resolution

- **Normal mode**: Existing `IncreaseOperation` semantics (commutative addition)
- **Dedup mode**: Each replica maintains its own HLL independently. On sync, HLL
  registers are merged via **max-merge**. Counter value is recomputed as
  `hll.count()` after merge.
  - HLL max-merge is commutative, associative, and idempotent — naturally
    suitable as a CRDT merge function

Sync flow example:

```
Client A: increase(1, {actor: "user-1"})
  → IncreaseOperation{value: 1, actor: "user-1"}
  → Local HLL adds hash("user-1") → new entry → value = 1

Client B: increase(1, {actor: "user-1"})  (same user)
  → IncreaseOperation{value: 1, actor: "user-1"}
  → Local HLL adds hash("user-1") → new entry → value = 1

On sync:
  A receives B's operation → HLL already has hash("user-1") → no value change
  B receives A's operation → HLL already has hash("user-1") → no value change
  Both sides: UV = 1 (correct)
```

### Snapshot Serialization

The current Counter snapshot path serializes `type` + `value` (as little-endian
bytes) + TimeTicket metadata through protobuf. For dedup mode, the HLL state
must also be persisted in snapshots.

#### Protobuf Schema Change

Extend the existing `JSONElement.Counter` message:

```protobuf
message Counter {
  ValueType type = 1;
  bytes value = 2;
  TimeTicket created_at = 3;
  TimeTicket moved_at = 4;
  TimeTicket removed_at = 5;
  // New fields for dedup mode:
  bool is_dedup = 6;
  bytes hll_registers = 7;  // Raw HLL register bytes (~16KB when dedup)
}
```

- `is_dedup`: Indicates whether this Counter uses dedup mode. Defaults to
  `false` for backward compatibility.
- `hll_registers`: Serialized HLL register array. Empty when `is_dedup` is
  false. When `is_dedup` is true, contains the raw register bytes (2^14 =
  16,384 registers, 1 byte each = ~16KB).

#### Serialization Path (Go)

In `api/converter/to_bytes.go`, `toCounter()` is extended:

```go
func toCounter(counter *crdt.Counter) (*api.JSONElement, error) {
    counterValue, _ := counter.Bytes()
    pbCounter := &api.JSONElement_Counter{
        Type:      toCounterType(counter.ValueType()),
        Value:     counterValue,
        CreatedAt: ToTimeTicket(counter.CreatedAt()),
        MovedAt:   ToTimeTicket(counter.MovedAt()),
        RemovedAt: ToTimeTicket(counter.RemovedAt()),
    }
    if counter.IsDedup() {
        pbCounter.IsDedup = true
        pbCounter.HllRegisters = counter.HLLBytes()
    }
    return &api.JSONElement{Body: &api.JSONElement_Counter_{Counter: pbCounter}}, nil
}
```

#### Deserialization Path (Go)

In `api/converter/from_bytes.go`, `fromJSONCounter()` is extended:

```go
func fromJSONCounter(pbCnt *api.JSONElement_Counter) (*crdt.Counter, error) {
    counterType, _ := fromCounterType(pbCnt.Type)
    counterValue, _ := crdt.CounterValueFromBytes(counterType, pbCnt.Value)
    counter, _ := crdt.NewCounter(counterType, counterValue, fromTimeTicket(pbCnt.CreatedAt))
    counter.SetMovedAt(fromTimeTicket(pbCnt.MovedAt))
    counter.SetRemovedAt(fromTimeTicket(pbCnt.RemovedAt))
    if pbCnt.IsDedup {
        counter.SetDedup(true)
        counter.RestoreHLL(pbCnt.HllRegisters)
    }
    return counter, nil
}
```

#### Serialization Path (JS SDK)

In `api/converter.ts`, `toCounter()` and `fromCounter()` follow the same
pattern — serialize/deserialize `isDedup` flag and `hllRegisters` bytes.

#### Backward Compatibility

- Existing snapshots have no `is_dedup` or `hll_registers` fields. Protobuf
  defaults (`false` and empty bytes) make them parse correctly as normal-mode
  Counters with no migration needed.
- Old servers/SDKs that do not know about the new fields will silently ignore
  them (standard protobuf forward compatibility).

#### Snapshot Size Impact

- Normal-mode Counter: unchanged (~20 bytes per Counter)
- Dedup-mode Counter: +16KB (HLL registers) + 1 byte (is_dedup flag)
- For a daily analytics Document with one PV Counter and one UV Counter, total
  snapshot size is ~16KB — well within MongoDB's 16MB BSON limit

### Daily Document Lifecycle

Document naming: `analytics-{pageId}-{YYYYMMDD}`

| Phase | Action | Actor |
|-------|--------|-------|
| Create | Created on first visit for the given date | Client |
| Use | PV/UV measurement, real-time queries | Client |
| Rollover | After midnight, switch to new date's Document | Client |
| Archive | Previous date's Document is detached, persisted on server | Automatic |

Date rollover:

- Client compares current date with Document date on each `increase` call
- If the date has changed, detach old Document and attach new one
- Time reference: **server time (UTC)** to prevent client clock skew issues

Daily reset is solved naturally — each date gets a fresh Document starting
from zero. Historical data is queryable by attaching the corresponding date's
Document.

### Large-Scale Considerations

These Documents are not for collaborative editing — they only perform Counter
increases. No Presence sync is needed and operations are simple.

Initial version scope:

1. Counter dedup mode with HLL
2. Daily Document pattern
3. Attach → increase → detach flow

Future optimizations (separate tasks):

| Strategy | Description |
|----------|-------------|
| Short-lived attach | Detach immediately after increase to minimize concurrent connections |
| Batch processing | Server batches operations within a short window (e.g., 1 second) |
| Sharding | Split into `analytics-{pageId}-{date}-{shard}`, merge on read. HLL merge preserves UV accuracy across shards |

Practical limits:

- Tens of thousands of concurrent connections to a single Document may cause
  bottlenecks — sharding is needed at that scale
- HLL ~2% error is stable at large scale but relative error can be significant
  at small scale (tens of UVs)

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Single Document bottleneck at tens of thousands of concurrent users | Initial version uses short-lived attach pattern. Sharding planned as follow-up |
| HLL relative error at small scale (tens of UVs) | Acceptable given approximate-count requirement. Precision is tunable |
| Client clock skew causing wrong-date Documents | Use server time (UTC) as the reference |
| Snapshot size increase (~16KB per dedup Counter) | Negligible relative to 16MB BSON limit. One daily Document holds ~16KB total |
| Backward compatibility of extended protobuf | New fields use protobuf defaults (false, empty bytes). Old clients/servers silently ignore unknown fields |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Extend Counter (not a new type) | Integrates naturally with existing Counter API. A separate type would require changes across all SDKs |
| HyperLogLog for dedup | ~16KB for millions of unique values, ~2% error. max-merge is a natural CRDT merge function |
| Daily Document pattern | No Counter reset needed. Historical queries possible. Clean lifecycle |
| Optional actor in IncreaseOperation | Backward compatible. No actor = normal mode |
| Server time (UTC) for dates | Prevents client clock skew issues |
| HLL registers in protobuf snapshot | Ensures dedup state survives server restarts and snapshot compaction. Uses standard protobuf forward/backward compatibility |
| No undo/redo for dedup Counter | HLL is append-only — no remove operation exists. Reverse operations would desync the HLL and scalar value. Dedup increases return no reverseOp |
| Enforce delta == 1 for dedup increase | HLL cardinality replaces the scalar value, so arbitrary deltas are meaningless. Enforcing 1 makes the API contract clear |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| New CRDT type (UniqueCounter) | Requires new type across all SDKs (Go, JS). Counter extension has smaller change footprint |
| Channel extension + server-side HLL | Channel is in-memory only — data lost on server restart. Document snapshots persist |
| Counter + server-side dedup | Server overriding CRDT semantics is architecturally inconsistent |
| Bloom Filter instead of HLL | Worse at cardinality estimation. Requires managing false positive rates |
| Exact Set-based counting | ~64 bytes per user ID. 1M UVs = ~64MB. HLL is far more memory-efficient |
| Separate HLL snapshot collection | Unnecessary complexity. 16KB per Counter fits easily in the existing snapshot format |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

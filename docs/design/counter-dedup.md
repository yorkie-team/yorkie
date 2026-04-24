---
title: counter-dedup
target-version: 0.7.7
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
- YSON round-trip preserves HLL register state (full fidelity) so that
  `storeRevision` and `CompactDocument` rebuild-compare succeed for dedup
  documents
- Support large-scale concurrent access (tens to hundreds of thousands)

### Non-Goals

- Exact UV counting (approximate counts are acceptable)
- Sharding, batching, and other large-scale optimizations (separate follow-up)
- Automated daily Document lifecycle management (client-managed)
- Undo/redo for dedup Counter — HyperLogLog is an append-only sketch with no
  remove operation, so once an actor is added it cannot be rolled back. Dedup
  Counter increases produce no reverse operation.
- `LongDedupCnt` support (does not exist in the crdt package yet; trivial to
  add later)
- Optimizing YSON human-readability for dedup counters (primary consumers are
  system paths: storeRevision, compaction)

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

Dedup mode is encoded in the Counter's ValueType rather than a runtime flag.
The ValueType `IntegerDedupCnt` signals dedup mode and automatically
initializes the internal HyperLogLog on creation.

```
CounterType:
  IntegerCnt         // normal 32-bit counter
  LongCnt            // normal 64-bit counter
  IntegerDedupCnt    // dedup 32-bit counter (auto-initializes HLL)

Counter
  ├── valueType: CounterType
  ├── value: number | Long (always 0 at creation for dedup types)
  └── hll: HyperLogLog (precision=14, ~16KB, only for dedup types)
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

Dedup mode is encoded in the ValueType enum, not as a separate field:

```protobuf
enum ValueType {
  // ...existing values...
  VALUE_TYPE_INTEGER_DEDUP_CNT = 14;
}

message Counter {
  ValueType type = 1;          // dedup types encode mode here
  bytes value = 2;
  TimeTicket created_at = 3;
  TimeTicket moved_at = 4;
  TimeTicket removed_at = 5;
  reserved 6;                  // was is_dedup, now in ValueType
  bytes hll_registers = 7;    // HLL register bytes (~16KB for dedup)
}
```

- `type`: `INTEGER_DEDUP_CNT` signals dedup mode. This is transmitted
  in both Set operations (counter creation) and snapshots, ensuring all
  replicas know the mode from creation.
- `hll_registers`: Serialized HLL register array. Empty for normal
  counters. For dedup counters, contains 2^14 = 16,384 registers (1
  byte each = ~16KB).

#### Serialization Path (Go)

In `api/converter/to_bytes.go`, `toCounter()` always sets `HllRegisters`
(nil for non-dedup counters):

```go
pbCounter := &api.JSONElement_Counter{
    Type:         toCounterType(counter.ValueType()),
    Value:        counterValue,
    CreatedAt:    ToTimeTicket(counter.CreatedAt()),
    MovedAt:      ToTimeTicket(counter.MovedAt()),
    RemovedAt:    ToTimeTicket(counter.RemovedAt()),
    HllRegisters: counter.HLLBytes(),
}
```

#### Deserialization Path (Go)

In `api/converter/from_bytes.go`, `fromJSONCounter()` detects dedup mode
from the ValueType (via `counter.IsDedup()`) and restores HLL if present:

```go
counter, _ := crdt.NewCounter(counterType, counterValue, createdAt)
counter.SetMovedAt(movedAt)
counter.SetRemovedAt(removedAt)
if counter.IsDedup() && len(pbCnt.GetHllRegisters()) > 0 {
    counter.RestoreHLL(pbCnt.GetHllRegisters())
}
```

#### Backward Compatibility

- Existing snapshots have no `hll_registers`. Protobuf defaults (empty
  bytes) make them parse correctly as normal-mode Counters with no
  migration needed.
- Old servers/SDKs that do not know about the new fields will silently
  ignore them (standard protobuf forward compatibility).

### YSON Serialization

The background path launched at `server/packs/pushpull.go:167` uses YSON
to serialize document state for `storeRevision` and snapshot compaction.
Without dedup counter support in the YSON path, `storeRevision` fails
with `ERROR marshal counter: unsupported element` and
`CompactDocument`'s rebuild-compare fails with `content mismatch after
rebuild`.

#### YSON Text Format

```
DedupCounter(Int(15),"base64encodedregisters...")
```

- `DedupCounter(` — outer type, distinct from `Counter(`
- `Int(15)` — derived count value (same inner-type token as normal
  Counter)
- `,"base64..."` — base64-encoded HLL registers (16,384 bytes raw →
  ~21,848 chars encoded)

No `HLL()` wrapper is used. The second argument is always the base64
HLL registers, so a wrapper adds unnecessary complexity.

#### Grammar: Regex Pre-substitution

The existing `preprocessTypeValues()` uses global `)` → `}` replacement,
which cannot produce valid JSON from `DedupCounter(Int(15),"base64...")`.
The comma-separated base64 string has no JSON key, so the global
replacement yields invalid output.

Solution: pre-substitute `DedupCounter(...)` patterns into complete JSON
before the general grammar runs.

```go
// Inside preprocessTypeValues, before general replacements:
re := regexp.MustCompile(`DedupCounter\(Int\((\d+)\),"([^"]+)"\)`)
data = re.ReplaceAllString(data,
    `{"type":"DedupCounter","counterType":"Int","value":$1,"hll":"$2"}`)
```

After pre-substitution, the `DedupCounter(...)` text is already valid
JSON with no remaining `(` or `)`, so the general grammar does not
interfere.

#### `yson.Counter` Struct Extension

```go
// pkg/document/yson/yson.go
type Counter struct {
    Type      crdt.CounterType
    Value     interface{}
    Registers []byte // HLL registers (dedup only; nil for normal counters)
}
```

#### Marshal

Add an `IntegerDedupCnt` case to `Counter.Marshal()`:

```go
case crdt.IntegerDedupCnt:
    encoded := base64.StdEncoding.EncodeToString(y.Registers)
    return fmt.Sprintf(`DedupCounter(Int(%v),"%s")`, y.Value, encoded), nil
```

#### Parse

Add a `"DedupCounter"` branch in `parseTypedValue()` that calls a new
`parseDedupCounter()`. This function receives the pre-substituted JSON
structure:

```json
{"type":"DedupCounter","counterType":"Int","value":15,"hll":"base64..."}
```

It extracts `counterType`, `value`, and `hll`, base64-decodes the
registers, and returns:

```go
Counter{
    Type:      crdt.IntegerDedupCnt,
    Value:     int32(value),
    Registers: decodedRegisters,
}
```

#### CRDT → YSON Conversion (`to_yson.go`)

Extend `toCounter()` to extract HLL bytes:

```go
func toCounter(counter *crdt.Counter) (Counter, error) {
    return Counter{
        Type:      counter.ValueType(),
        Value:     counter.Value(),
        Registers: counter.HLLBytes(), // nil for non-dedup
    }, nil
}
```

#### SetYSON: HLL Restoration

Pass HLL registers through `setNewCounter`/`AddNewCounter` via a
variadic parameter. HLL is restored inside the creator function, before
`DeepCopy` captures the element for the operation log:

```go
case yson.Counter:
    p.setNewCounter(k, y.Type, y.Value, y.Registers)
```

HLL restoration must happen inside the creator closure (before
`DeepCopy`), not after the constructor returns. Yorkie's clone/execute
architecture deep-copies the counter inside `setInternal`/`addInternal`,
so the operation's copy must already carry the restored HLL. The
existing `crdt.Counter.RestoreHLL` restores the register array and calls
`recomputeValue()`, so the counter's value is correctly set even though
`crdt.NewCounter(IntegerDedupCnt, ...)` always initializes `value=0`.

#### YSON Data Flow

```
Store (storeRevision):
  crdt.Counter{value=15, hll=[...16KB...]}
  → toCounter() → yson.Counter{Value=15, Registers=[...]}
  → Marshal() → DedupCounter(Int(15),"base64...")
  → saved to DB

Restore (compaction rebuild):
  DedupCounter(Int(15),"base64...")
  → preprocessDedupCounter() → JSON
  → parseDedupCounter() → yson.Counter{Value=15, Registers=[...]}
  → SetYSONElement → setNewCounter(value=0) → RestoreHLL → recomputeValue(value=15)
  → Marshal() → DedupCounter(Int(15),"base64...")  ← matches original
```

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
| Snapshot/YSON size increase (~16KB per dedup Counter) | Negligible relative to 16MB BSON limit. YSON base64 adds ~22KB but primary consumers are system paths, not humans |
| Backward compatibility of extended protobuf/YSON | Protobuf defaults (empty bytes) preserve normal counters. No existing YSON output contains `DedupCounter(...)` — introducing it is non-breaking |
| Regex pre-substitution diverges from the general grammar style | Unavoidable: the second argument (bare base64 string) is incompatible with the global `)` → `}` replacement. The regex is simple and scoped to `DedupCounter` only |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Extend Counter (not a new type) | Integrates naturally with existing Counter API. A separate type would require changes across all SDKs |
| HyperLogLog for dedup | ~16KB for millions of unique values, ~2% error. max-merge is a natural CRDT merge function |
| Daily Document pattern | No Counter reset needed. Historical queries possible. Clean lifecycle |
| Optional actor in IncreaseOperation | Backward compatible. No actor = normal mode |
| Server time (UTC) for dates | Prevents client clock skew issues |
| HLL registers in protobuf and YSON | Ensures dedup state survives server restarts, snapshot compaction, and revision restore. Both paths provide full HLL fidelity |
| No undo/redo for dedup Counter | HLL is append-only — no remove operation exists. Reverse operations would desync the HLL and scalar value. Dedup increases return no reverseOp |
| Enforce delta == 1 for dedup increase | HLL cardinality replaces the scalar value, so arbitrary deltas are meaningless. Enforcing 1 makes the API contract clear |
| No `HLL()` wrapper — bare base64 string | The second argument is always base64 HLL registers. A wrapper adds grammar complexity for no benefit |
| Regex pre-substitution for YSON | The general grammar's global `)` → `}` replacement cannot produce valid JSON for `DedupCounter`'s compound structure. Pre-substitution is the simplest solution |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| New CRDT type (UniqueCounter) | Requires new type across all SDKs (Go, JS). Counter extension has smaller change footprint |
| Channel extension + server-side HLL | Channel is in-memory only — data lost on server restart. Document snapshots persist |
| Counter + server-side dedup | Server overriding CRDT semantics is architecturally inconsistent |
| Bloom Filter instead of HLL | Worse at cardinality estimation. Requires managing false positive rates |
| Exact Set-based counting | ~64 bytes per user ID. 1M UVs = ~64MB. HLL is far more memory-efficient |
| Separate HLL snapshot collection | Unnecessary complexity. 16KB per Counter fits easily in the existing snapshot format |
| Minimal YSON fix + bypass compaction for dedup | Revision restore still loses HLL state. Split serialization strategy (YSON for revisions, protobuf for compaction) is harder to reason about |
| `HLL("base64")` wrapper in YSON text | Unnecessary — second argument is always base64. Adds a grammar table entry for no benefit |
| Grammar table only (no regex) for YSON | Global `)` → `}` replacement conflicts with the bare base64 string inside `DedupCounter(...)`. Cannot produce valid JSON |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

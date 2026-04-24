---
title: yson-dedup-counter
target-version: 0.7.7
---

# YSON DedupCounter Serialization

## Problem

[#1733](https://github.com/yorkie-team/yorkie/pull/1733) added
`IntegerDedupCnt` to `pkg/document/crdt/counter.go` but did not update
the YSON serialization path in `pkg/document/yson`. This leaves two gaps
on the background path launched at `server/packs/pushpull.go:167`:

**Part 1 — storeRevision fails silently.** `Counter.Marshal()`,
`parseCounter()`, and the YSON grammar table don't handle dedup
counters. Every pushpull against a dedup-containing document causes
`storeRevision` to fail with `ERROR marshal counter: unsupported
element`. The client receives 200 OK while the server logs fill with
errors.

**Part 2 — HLL state lost across YSON round-trip.** Even after Part 1 is
fixed, YSON's `Counter` representation captures only the derived count
value, not the HLL register bitmap (~16KB). This breaks
`CompactDocument`'s rebuild-compare invariant (`content mismatch after
rebuild`) and means revision restore cannot recover dedup history.

Related issue:
[yorkie-team/yorkie#1773](https://github.com/yorkie-team/yorkie/issues/1773)

### Goals

- `storeRevision` completes without errors for documents containing
  dedup counters
- YSON round-trip preserves HLL register state (full fidelity)
- `CompactDocument` rebuild-compare succeeds for dedup documents
- Revision restore recovers dedup history

### Non-Goals

- `LongDedupCnt` support (does not exist in the crdt package yet;
  trivial to add later)
- Optimizing YSON human-readability for dedup counters (primary
  consumers are system paths: storeRevision, compaction)
- Migrating existing stored revision data (no `DedupCounter` YSON
  output exists today — current code errors before producing any)

## Design

### YSON Text Format

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

### Grammar: Regex Pre-substitution

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

### `yson.Counter` Struct Extension

```go
// pkg/document/yson/yson.go
type Counter struct {
    Type      crdt.CounterType
    Value     interface{}
    Registers []byte // HLL registers (dedup only; nil for normal counters)
}
```

### Marshal

Add an `IntegerDedupCnt` case to `Counter.Marshal()`:

```go
case crdt.IntegerDedupCnt:
    encoded := base64.StdEncoding.EncodeToString(y.Registers)
    return fmt.Sprintf(`DedupCounter(Int(%v),"%s")`, y.Value, encoded), nil
```

### Parse

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

### CRDT → YSON Conversion (`to_yson.go`)

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

### SetYSON: HLL Restoration

In `SetYSONElement()` (Counter branch), restore HLL after creating the
counter:

```go
case yson.Counter:
    c := p.setNewCounter(k, y.Type, y.Value)
    if len(y.Registers) > 0 {
        c.RestoreHLL(y.Registers)
    }
```

This requires adding a `RestoreHLL(data []byte) error` method to
`pkg/document/json/counter.go` that delegates to
`crdt.Counter.RestoreHLL()`. The existing `RestoreHLL` restores the
register array and calls `recomputeValue()`, so the counter's value is
correctly set even though `crdt.NewCounter(IntegerDedupCnt, ...)` always
initializes `value=0`.

### Data Flow

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

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| base64 HLL degrades YSON readability (~22KB per dedup counter) | Primary YSON consumers are system paths (storeRevision, compaction), not humans |
| Regex pre-substitution diverges from the general grammar style | Unavoidable: the second argument (bare base64 string) is incompatible with the global `)` → `}` replacement. The regex is simple and scoped to `DedupCounter` only |
| base64 encode/decode performance | 16KB base64 conversion is negligible. storeRevision and compaction are background operations |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Include HLL base64 in YSON text | Provides the same fidelity as protobuf. Both compaction and revision restore work correctly |
| No `HLL()` wrapper — bare base64 string | The second argument is always base64 HLL registers. A wrapper adds grammar complexity for no benefit |
| Regex pre-substitution | The general grammar's global `)` → `}` replacement cannot produce valid JSON for `DedupCounter`'s compound structure. Pre-substitution is the simplest solution |
| No `LongDedupCnt` | Does not exist in crdt package. YAGNI — adding it later requires one `case` per function |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Minimal YSON fix + bypass compaction for dedup (issue Option B) | Revision restore still loses HLL state. Split serialization strategy (YSON for revisions, protobuf for compaction) is harder to reason about |
| Document the limitation only (issue Option C) | Changes collection grows unbounded for long-lived dedup documents. Shifts operational burden to users |
| `HLL("base64")` wrapper in YSON text | Unnecessary — second argument is always base64. Adds a grammar table entry for no benefit |
| Grammar table only (no regex) | Global `)` → `}` replacement conflicts with the bare base64 string inside `DedupCounter(...)`. Cannot produce valid JSON |

## Changed Files

| File | Change |
|------|--------|
| `pkg/document/yson/yson.go` | Add `Registers` field to Counter struct. Add `IntegerDedupCnt` case to `Marshal()`. Add `preprocessDedupCounter()` regex. Add `parseDedupCounter()` |
| `pkg/document/yson/to_yson.go` | Extract `HLLBytes()` in `toCounter()` |
| `pkg/document/json/object.go` | Call `RestoreHLL` in `SetYSONElement` Counter branch |
| `pkg/document/json/counter.go` | Add `RestoreHLL(data []byte) error` method |
| `pkg/document/yson/yson_test.go` | DedupCounter marshal/unmarshal round-trip unit tests |
| `test/integration/` | storeRevision and compaction integration tests for dedup documents |

## Backward Compatibility

Existing YSON outputs never contained `DedupCounter(...)` — under the
current code, any attempt to marshal a dedup counter fails before
producing output. Therefore no stored data uses the new token and
introducing it is non-breaking.

Existing `Counter(Int(...))` and `Counter(Long(...))` paths are
unchanged.

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

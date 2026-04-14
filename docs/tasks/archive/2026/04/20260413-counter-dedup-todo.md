**Created**: 2026-04-13

# Counter Dedup Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a dedup mode to Counter that uses HyperLogLog to ignore duplicate actors, enabling UV measurement.

**Architecture:** Extend Counter with an optional HLL, extend IncreaseOperation with an optional actor field, update protobuf schema with `is_dedup` and `hll_registers` fields, and update all serialization paths (snapshot and operation) in both Go server and JS SDK.

**Tech Stack:** Go (server), TypeScript (JS SDK), Protocol Buffers, HyperLogLog

**Spec:** `docs/design/counter-dedup.md`

---

## File Map

### Go Server

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `pkg/document/crdt/hll.go` | HyperLogLog implementation (precision=14, max-merge, serialization) |
| Create | `pkg/document/crdt/hll_test.go` | HLL unit tests |
| Modify | `pkg/document/crdt/counter.go` | Add `isDedup`, `hll` fields, dedup-aware `Increase`, `HLLBytes`, `RestoreHLL` |
| Modify | `pkg/document/crdt/counter_test.go` | Add dedup mode tests |
| Modify | `pkg/document/json/counter.go` | Add dedup-aware `Increase` with actor parameter |
| Modify | `pkg/document/operations/increase.go` | Add optional `actor` field |
| Modify | `api/yorkie/v1/resources.proto` | Add `is_dedup`, `hll_registers` to Counter; add `actor` to Increase |
| Modify | `api/converter/to_bytes.go` | Serialize HLL state in `toCounter()` |
| Modify | `api/converter/from_bytes.go` | Deserialize HLL state in `fromJSONCounter()` |
| Modify | `api/converter/to_pb.go` | Serialize actor in `toIncrease()` |
| Modify | `api/converter/from_pb.go` | Deserialize actor in `fromIncrease()` |
| Create | `test/integration/counter_dedup_test.go` | Integration tests for dedup Counter |

### JS SDK

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `packages/sdk/src/document/crdt/hll.ts` | HyperLogLog implementation (matching Go) |
| Modify | `packages/sdk/src/document/crdt/counter.ts` | Add `isDedup`, `hll` fields, dedup-aware `increase` |
| Modify | `packages/sdk/src/document/json/counter.ts` | Add dedup-aware `increase` with actor option |
| Modify | `packages/sdk/src/document/operation/increase_operation.ts` | Add optional `actor` field |
| Modify | `packages/sdk/src/api/converter.ts` | Serialize/deserialize HLL state and actor |

---

### Task 1: HyperLogLog Implementation (Go)

**Files:**
- Create: `pkg/document/crdt/hll.go`
- Create: `pkg/document/crdt/hll_test.go`

- [ ] **Step 1: Write failing tests for HLL**

Create `pkg/document/crdt/hll_test.go`:

```go
// Copyright 2026 The Yorkie Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package crdt_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

func TestHLL(t *testing.T) {
	t.Run("new HLL has zero count", func(t *testing.T) {
		hll := crdt.NewHLL()
		assert.Equal(t, uint64(0), hll.Count())
	})

	t.Run("add single element returns true", func(t *testing.T) {
		hll := crdt.NewHLL()
		added := hll.Add("user-1")
		assert.True(t, added)
		assert.Equal(t, uint64(1), hll.Count())
	})

	t.Run("add duplicate element returns false", func(t *testing.T) {
		hll := crdt.NewHLL()
		hll.Add("user-1")
		added := hll.Add("user-1")
		assert.False(t, added)
		assert.Equal(t, uint64(1), hll.Count())
	})

	t.Run("count many unique elements within error margin", func(t *testing.T) {
		hll := crdt.NewHLL()
		n := 100000
		for i := 0; i < n; i++ {
			hll.Add(fmt.Sprintf("user-%d", i))
		}
		count := hll.Count()
		errorRate := math.Abs(float64(count)-float64(n)) / float64(n)
		assert.Less(t, errorRate, 0.05) // within 5%
	})

	t.Run("merge two HLLs", func(t *testing.T) {
		hll1 := crdt.NewHLL()
		hll2 := crdt.NewHLL()

		hll1.Add("user-1")
		hll1.Add("user-2")

		hll2.Add("user-2")
		hll2.Add("user-3")

		hll1.Merge(hll2)
		assert.Equal(t, uint64(3), hll1.Count())
	})

	t.Run("merge is idempotent", func(t *testing.T) {
		hll1 := crdt.NewHLL()
		hll2 := crdt.NewHLL()

		hll1.Add("user-1")
		hll2.Add("user-1")

		hll1.Merge(hll2)
		assert.Equal(t, uint64(1), hll1.Count())
	})

	t.Run("serialize and restore", func(t *testing.T) {
		hll := crdt.NewHLL()
		hll.Add("user-1")
		hll.Add("user-2")
		hll.Add("user-3")
		originalCount := hll.Count()

		bytes := hll.Bytes()
		assert.Equal(t, 1<<14, len(bytes)) // precision 14 = 16384 registers

		restored := crdt.NewHLL()
		restored.Restore(bytes)
		assert.Equal(t, originalCount, restored.Count())
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd 03_projects/yorkie && go test ./pkg/document/crdt/ -run TestHLL -v`
Expected: compilation error — `crdt.NewHLL` undefined

- [ ] **Step 3: Implement HLL**

Create `pkg/document/crdt/hll.go`:

```go
// Copyright 2026 The Yorkie Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package crdt

import (
	"hash"
	"hash/fnv"
	"math"
)

const (
	// hllPrecision is the number of bits used for register indexing.
	// 2^14 = 16384 registers, ~16KB memory, ~2% standard error.
	hllPrecision = 14

	// hllRegisterCount is the number of registers.
	hllRegisterCount = 1 << hllPrecision
)

// HLL is a HyperLogLog probabilistic cardinality estimator.
// It supports Add, Count, Merge (max-merge), and serialization.
type HLL struct {
	registers [hllRegisterCount]uint8
	hasher    hash.Hash64
}

// NewHLL creates a new HLL instance.
func NewHLL() *HLL {
	return &HLL{
		hasher: fnv.New64a(),
	}
}

// Add inserts a value into the HLL. Returns true if the value changed a
// register (likely new), false otherwise (likely duplicate).
func (h *HLL) Add(value string) bool {
	hash := h.hash(value)
	idx := hash >> (64 - hllPrecision)
	remaining := (hash << hllPrecision) | (1 << (hllPrecision - 1))
	rho := uint8(countLeadingZeros(remaining) + 1)

	if rho > h.registers[idx] {
		h.registers[idx] = rho
		return true
	}
	return false
}

// Count returns the estimated cardinality.
func (h *HLL) Count() uint64 {
	m := float64(hllRegisterCount)
	alpha := 0.7213 / (1.0 + 1.079/m)

	sum := 0.0
	zeros := 0
	for _, val := range h.registers {
		sum += math.Pow(2, -float64(val))
		if val == 0 {
			zeros++
		}
	}

	estimate := alpha * m * m / sum

	// Small range correction
	if estimate <= 2.5*m && zeros > 0 {
		estimate = m * math.Log(m/float64(zeros))
	}

	return uint64(math.Round(estimate))
}

// Merge performs max-merge with another HLL. This operation is commutative,
// associative, and idempotent — suitable as a CRDT merge function.
func (h *HLL) Merge(other *HLL) {
	for i := range h.registers {
		if other.registers[i] > h.registers[i] {
			h.registers[i] = other.registers[i]
		}
	}
}

// Bytes returns the raw register array for serialization.
func (h *HLL) Bytes() []byte {
	return h.registers[:]
}

// Restore loads register data from bytes.
func (h *HLL) Restore(data []byte) {
	copy(h.registers[:], data)
}

// hash returns a 64-bit hash of the given value.
func (h *HLL) hash(value string) uint64 {
	h.hasher.Reset()
	// fnv hash.Write never returns an error.
	h.hasher.Write([]byte(value))
	return h.hasher.Sum64()
}

// countLeadingZeros counts the number of leading zeros in a 64-bit integer.
func countLeadingZeros(x uint64) int {
	if x == 0 {
		return 64
	}
	n := 0
	for (x & (1 << 63)) == 0 {
		n++
		x <<= 1
	}
	return n
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd 03_projects/yorkie && go test ./pkg/document/crdt/ -run TestHLL -v`
Expected: all 7 tests PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/document/crdt/hll.go pkg/document/crdt/hll_test.go
git commit -m "Add HyperLogLog implementation for Counter dedup mode"
```

---

### Task 2: Extend CRDT Counter with Dedup Mode (Go)

**Files:**
- Modify: `pkg/document/crdt/counter.go:54-88` (Counter struct and NewCounter)
- Modify: `pkg/document/crdt/counter.go:141-144` (DeepCopy)
- Modify: `pkg/document/crdt/counter.go:197-219` (Increase)
- Modify: `pkg/document/crdt/counter_test.go`

- [ ] **Step 1: Write failing tests for dedup Counter**

Append to `pkg/document/crdt/counter_test.go`:

```go
func TestCounterDedup(t *testing.T) {
	t.Run("create dedup counter", func(t *testing.T) {
		counter, err := crdt.NewCounter(crdt.IntegerCnt, int32(0), dummyTicket)
		assert.NoError(t, err)
		counter.SetDedup(true)
		assert.True(t, counter.IsDedup())
		assert.Equal(t, "0", counter.Marshal())
	})

	t.Run("dedup increase with new actor", func(t *testing.T) {
		counter, err := crdt.NewCounter(crdt.IntegerCnt, int32(0), dummyTicket)
		assert.NoError(t, err)
		counter.SetDedup(true)

		operand, err := crdt.NewPrimitive(int32(1), dummyTicket)
		assert.NoError(t, err)

		_, err = counter.IncreaseDedup(operand, "user-1")
		assert.NoError(t, err)
		assert.Equal(t, "1", counter.Marshal())
	})

	t.Run("dedup increase with duplicate actor is ignored", func(t *testing.T) {
		counter, err := crdt.NewCounter(crdt.IntegerCnt, int32(0), dummyTicket)
		assert.NoError(t, err)
		counter.SetDedup(true)

		operand, err := crdt.NewPrimitive(int32(1), dummyTicket)
		assert.NoError(t, err)

		counter.IncreaseDedup(operand, "user-1")
		_, err = counter.IncreaseDedup(operand, "user-1")
		assert.NoError(t, err)
		assert.Equal(t, "1", counter.Marshal())
	})

	t.Run("dedup increase with different actors", func(t *testing.T) {
		counter, err := crdt.NewCounter(crdt.IntegerCnt, int32(0), dummyTicket)
		assert.NoError(t, err)
		counter.SetDedup(true)

		operand, err := crdt.NewPrimitive(int32(1), dummyTicket)
		assert.NoError(t, err)

		counter.IncreaseDedup(operand, "user-1")
		counter.IncreaseDedup(operand, "user-2")
		counter.IncreaseDedup(operand, "user-3")
		assert.Equal(t, "3", counter.Marshal())
	})

	t.Run("dedup deep copy preserves HLL state", func(t *testing.T) {
		counter, err := crdt.NewCounter(crdt.IntegerCnt, int32(0), dummyTicket)
		assert.NoError(t, err)
		counter.SetDedup(true)

		operand, err := crdt.NewPrimitive(int32(1), dummyTicket)
		assert.NoError(t, err)
		counter.IncreaseDedup(operand, "user-1")
		counter.IncreaseDedup(operand, "user-2")

		copied, err := counter.DeepCopy()
		assert.NoError(t, err)
		copiedCounter := copied.(*crdt.Counter)
		assert.True(t, copiedCounter.IsDedup())
		assert.Equal(t, "2", copiedCounter.Marshal())

		// Original and copy are independent
		counter.IncreaseDedup(operand, "user-3")
		assert.Equal(t, "3", counter.Marshal())
		assert.Equal(t, "2", copiedCounter.Marshal())
	})

	t.Run("HLL bytes round-trip", func(t *testing.T) {
		counter, err := crdt.NewCounter(crdt.IntegerCnt, int32(0), dummyTicket)
		assert.NoError(t, err)
		counter.SetDedup(true)

		operand, err := crdt.NewPrimitive(int32(1), dummyTicket)
		assert.NoError(t, err)
		counter.IncreaseDedup(operand, "user-1")
		counter.IncreaseDedup(operand, "user-2")

		hllBytes := counter.HLLBytes()
		assert.NotNil(t, hllBytes)

		restored, err := crdt.NewCounter(crdt.IntegerCnt, int32(0), dummyTicket)
		assert.NoError(t, err)
		restored.SetDedup(true)
		restored.RestoreHLL(hllBytes)
		assert.Equal(t, "2", restored.Marshal())
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd 03_projects/yorkie && go test ./pkg/document/crdt/ -run TestCounterDedup -v`
Expected: compilation error — `SetDedup`, `IsDedup`, `IncreaseDedup`, `HLLBytes`, `RestoreHLL` undefined

- [ ] **Step 3: Add dedup fields and methods to Counter**

In `pkg/document/crdt/counter.go`, add the `isDedup` and `hll` fields to the Counter struct:

```go
// Counter represents changeable number data type.
type Counter struct {
	valueType CounterType
	value     interface{}
	createdAt *time.Ticket
	movedAt   *time.Ticket
	removedAt *time.Ticket
	isDedup   bool
	hll       *HLL
}
```

Add the following methods after the existing `IsNumericType()` method:

```go
// SetDedup enables or disables dedup mode. When enabled, the Counter
// maintains an internal HLL for duplicate detection.
func (p *Counter) SetDedup(dedup bool) {
	p.isDedup = dedup
	if dedup && p.hll == nil {
		p.hll = NewHLL()
	}
}

// IsDedup returns whether this Counter is in dedup mode.
func (p *Counter) IsDedup() bool {
	return p.isDedup
}

// IncreaseDedup increases the counter only if the actor has not been seen
// before. Returns the counter and nil error on success.
func (p *Counter) IncreaseDedup(v *Primitive, actor string) (*Counter, error) {
	if !p.isDedup || p.hll == nil {
		return p.Increase(v)
	}

	if !p.IsNumericType() || !v.IsNumericType() {
		return nil, ErrUnsupportedType
	}

	if p.hll.Add(actor) {
		p.recomputeValue()
	}

	return p, nil
}

// HLLBytes returns the serialized HLL registers. Returns nil if not in dedup mode.
func (p *Counter) HLLBytes() []byte {
	if p.hll == nil {
		return nil
	}
	return p.hll.Bytes()
}

// RestoreHLL restores the HLL state from bytes and recomputes the counter value.
func (p *Counter) RestoreHLL(data []byte) {
	if p.hll == nil {
		p.hll = NewHLL()
	}
	p.hll.Restore(data)
	p.recomputeValue()
}

// recomputeValue sets the counter value from the HLL count.
func (p *Counter) recomputeValue() {
	count := p.hll.Count()
	switch p.valueType {
	case IntegerCnt:
		p.value = int32(count)
	case LongCnt:
		p.value = int64(count)
	}
}
```

Update `DeepCopy` to preserve HLL state:

```go
// DeepCopy copies itself deeply.
func (p *Counter) DeepCopy() (Element, error) {
	counter := *p
	if p.isDedup && p.hll != nil {
		counter.hll = NewHLL()
		counter.hll.Restore(p.hll.Bytes())
	}
	return &counter, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd 03_projects/yorkie && go test ./pkg/document/crdt/ -run TestCounterDedup -v`
Expected: all 6 tests PASS

- [ ] **Step 5: Run all existing Counter tests to verify no regression**

Run: `cd 03_projects/yorkie && go test ./pkg/document/crdt/ -run TestCounter -v`
Expected: all existing tests PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/document/crdt/counter.go pkg/document/crdt/counter_test.go
git commit -m "Add dedup mode to CRDT Counter with HLL-based duplicate detection"
```

---

### Task 3: Extend IncreaseOperation with Actor Field (Go)

**Files:**
- Modify: `pkg/document/operations/increase.go`

- [ ] **Step 1: Add actor field to Increase operation**

In `pkg/document/operations/increase.go`, add the `actor` field to the `Increase` struct and update the constructor:

Add a new `actor` field (type `string`) to the `Increase` struct.

Add a new constructor `NewIncreaseWithActor` that takes the same params as `NewIncrease` plus an `actor string` parameter:

```go
// NewIncreaseWithActor creates a new instance of Increase with an actor for dedup mode.
func NewIncreaseWithActor(
	parentCreatedAt *time.Ticket,
	value Element,
	executedAt *time.Ticket,
	actor string,
) *Increase {
	return &Increase{
		parentCreatedAt: parentCreatedAt,
		value:           value,
		executedAt:      executedAt,
		actor:           actor,
	}
}
```

Add an `Actor()` accessor:

```go
// Actor returns the actor for dedup mode. Empty string means normal mode.
func (o *Increase) Actor() string {
	return o.actor
}
```

Update `Execute()` to handle dedup mode: when `o.actor` is non-empty and the target is a Counter with dedup mode, call `IncreaseDedup` instead of `Increase`:

```go
// Execute executes this operation on the given document(`root`).
func (o *Increase) Execute(root *crdt.Root) error {
	parent := root.FindByCreatedAt(o.parentCreatedAt)
	cnt, ok := parent.(*crdt.Counter)
	if !ok {
		return ErrNotApplicableDataType
	}

	if o.actor != "" && cnt.IsDedup() {
		if _, err := cnt.IncreaseDedup(o.value.(*crdt.Primitive), o.actor); err != nil {
			return err
		}
	} else {
		if _, err := cnt.Increase(o.value.(*crdt.Primitive)); err != nil {
			return err
		}
	}

	return nil
}
```

- [ ] **Step 2: Run existing tests**

Run: `cd 03_projects/yorkie && go test ./pkg/document/... -v`
Expected: all existing tests PASS (backward compatible — existing code uses `NewIncrease` which leaves actor empty)

- [ ] **Step 3: Commit**

```bash
git add pkg/document/operations/increase.go
git commit -m "Add optional actor field to IncreaseOperation for dedup mode"
```

---

### Task 4: Extend JSON Counter Proxy with Dedup Increase (Go)

**Files:**
- Modify: `pkg/document/json/counter.go`

- [ ] **Step 1: Add IncreaseDedup method to JSON Counter**

Add a new method `IncreaseDedup` to `pkg/document/json/counter.go` that takes a numeric value and an actor string:

```go
// IncreaseDedup adds an increase operation with dedup. If the actor has
// already been counted, the increase is ignored.
func (p *Counter) IncreaseDedup(v interface{}, actor string) *Counter {
	if !isAllowedOperand(v) {
		panic("unsupported type")
	}
	var primitive *crdt.Primitive
	var err error
	ticket := p.context.IssueTimeTicket()

	value, kind := convertAssertableOperand(v)
	isInt := kind == reflect.Int
	switch p.ValueType() {
	case crdt.LongCnt:
		if isInt {
			primitive, err = crdt.NewPrimitive(int64(value.(int)), ticket)
		} else {
			primitive, err = crdt.NewPrimitive(int64(value.(float64)), ticket)
		}
	case crdt.IntegerCnt:
		if isInt {
			primitive, err = crdt.NewPrimitive(int32(value.(int)), ticket)
		} else {
			primitive, err = crdt.NewPrimitive(int32(value.(float64)), ticket)
		}
	default:
		panic("unsupported type")
	}
	if err != nil {
		panic(err)
	}
	if _, err := p.Counter.IncreaseDedup(primitive, actor); err != nil {
		panic(err)
	}

	p.context.Push(operations.NewIncreaseWithActor(
		p.CreatedAt(),
		primitive,
		ticket,
		actor,
	))

	return p
}
```

- [ ] **Step 2: Run existing tests**

Run: `cd 03_projects/yorkie && go test ./pkg/document/... -v`
Expected: all existing tests PASS

- [ ] **Step 3: Commit**

```bash
git add pkg/document/json/counter.go
git commit -m "Add IncreaseDedup method to JSON Counter proxy"
```

---

### Task 5: Update Protobuf Schema

**Files:**
- Modify: `api/yorkie/v1/resources.proto`

- [ ] **Step 1: Add dedup fields to Counter message**

In `api/yorkie/v1/resources.proto`, extend the `JSONElement.Counter` message:

```protobuf
message Counter {
  ValueType type = 1;
  bytes value = 2;
  TimeTicket created_at = 3;
  TimeTicket moved_at = 4;
  TimeTicket removed_at = 5;
  bool is_dedup = 6;
  bytes hll_registers = 7;
}
```

- [ ] **Step 2: Add actor field to Operation.Increase message**

In the same file, extend the `Operation.Increase` message:

```protobuf
message Increase {
  TimeTicket parent_created_at = 1;
  JSONElementSimple value = 2;
  TimeTicket executed_at = 3;
  string actor = 4;
}
```

- [ ] **Step 3: Regenerate protobuf code**

Run: `cd 03_projects/yorkie && make proto`
Expected: protobuf Go code regenerated successfully

- [ ] **Step 4: Commit**

```bash
git add api/yorkie/v1/resources.proto api/yorkie/v1/
git commit -m "Add dedup fields to Counter and actor to Increase in protobuf schema"
```

---

### Task 6: Update Snapshot Serialization (Go Converter)

**Files:**
- Modify: `api/converter/to_bytes.go` (toCounter function)
- Modify: `api/converter/from_bytes.go` (fromJSONCounter function)

- [ ] **Step 1: Update toCounter to serialize HLL state**

In `api/converter/to_bytes.go`, update the `toCounter` function to include dedup fields when the Counter is in dedup mode:

After setting the existing fields (Type, Value, CreatedAt, MovedAt, RemovedAt), add:

```go
if counter.IsDedup() {
    pbCounter.IsDedup = true
    pbCounter.HllRegisters = counter.HLLBytes()
}
```

The exact field names depend on the generated protobuf Go code. Check the generated struct after `make proto` for the correct field names.

- [ ] **Step 2: Update fromJSONCounter to deserialize HLL state**

In `api/converter/from_bytes.go`, update `fromJSONCounter` to restore dedup state:

After the existing counter creation and timestamp restoration, add:

```go
if pbCnt.IsDedup {
    counter.SetDedup(true)
    if len(pbCnt.HllRegisters) > 0 {
        counter.RestoreHLL(pbCnt.HllRegisters)
    }
}
```

- [ ] **Step 3: Run tests**

Run: `cd 03_projects/yorkie && go test ./api/converter/ -v`
Expected: all existing converter tests PASS

- [ ] **Step 4: Commit**

```bash
git add api/converter/to_bytes.go api/converter/from_bytes.go
git commit -m "Serialize and deserialize HLL state in Counter snapshots"
```

---

### Task 7: Update Operation Serialization (Go Converter)

**Files:**
- Modify: `api/converter/to_pb.go` (toIncrease function)
- Modify: `api/converter/from_pb.go` (fromIncrease function)

- [ ] **Step 1: Update toIncrease to serialize actor**

In `api/converter/to_pb.go`, update `toIncrease` to include the actor field:

After the existing fields (ParentCreatedAt, Value, ExecutedAt), add:

```go
Actor: increase.Actor(),
```

- [ ] **Step 2: Update fromIncrease to deserialize actor**

In `api/converter/from_pb.go`, update `fromIncrease`:

When `pbInc.Actor` is non-empty, use `NewIncreaseWithActor` instead of `NewIncrease`:

```go
if pbInc.Actor != "" {
    return operations.NewIncreaseWithActor(
        parentCreatedAt,
        elem,
        executedAt,
        pbInc.Actor,
    ), nil
}
return operations.NewIncrease(
    parentCreatedAt,
    elem,
    executedAt,
), nil
```

- [ ] **Step 3: Run tests**

Run: `cd 03_projects/yorkie && go test ./api/converter/ -v`
Expected: all converter tests PASS

- [ ] **Step 4: Commit**

```bash
git add api/converter/to_pb.go api/converter/from_pb.go
git commit -m "Serialize and deserialize actor field in IncreaseOperation"
```

---

### Task 8: Go Integration Test

**Files:**
- Create: `test/integration/counter_dedup_test.go`

- [ ] **Step 1: Write integration test**

Create `test/integration/counter_dedup_test.go` with build tag `integration`:

```go
//go:build integration

// Copyright 2026 The Yorkie Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestCounterDedup(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	ctx := context.Background()

	t.Run("dedup counter ignores duplicate actors across clients", func(t *testing.T) {
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		// Client 1: create dedup counter and increase with user-1
		assert.NoError(t, d1.Update(func(root *json.Object, p *json.Presence) error {
			cnt := root.SetNewCounter("uv", crdt.IntegerCnt)
			cnt.Counter.SetDedup(true)
			cnt.IncreaseDedup(1, "user-1")
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))

		// Client 2: attach and sync
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		// Client 2: increase with same user-1 (should be ignored)
		assert.NoError(t, d2.Update(func(root *json.Object, p *json.Presence) error {
			root.GetCounter("uv").IncreaseDedup(1, "user-1")
			return nil
		}))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		// Both clients should see UV = 1
		assert.Equal(t, `{"uv":1}`, d1.Marshal())
		assert.Equal(t, `{"uv":1}`, d2.Marshal())
	})

	t.Run("dedup counter counts distinct actors", func(t *testing.T) {
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *json.Presence) error {
			cnt := root.SetNewCounter("uv", crdt.IntegerCnt)
			cnt.Counter.SetDedup(true)
			cnt.IncreaseDedup(1, "user-1")
			cnt.IncreaseDedup(1, "user-2")
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d2.Update(func(root *json.Object, p *json.Presence) error {
			root.GetCounter("uv").IncreaseDedup(1, "user-3")
			return nil
		}))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		// Both clients should see UV = 3
		assert.Equal(t, `{"uv":3}`, d1.Marshal())
		assert.Equal(t, `{"uv":3}`, d2.Marshal())
	})
}
```

Note: The exact test helper APIs (`activeClients`, `helper.TestDocKey`, `root.SetNewCounter`, `root.GetCounter`) must match the existing integration test patterns. Check `test/integration/counter_test.go` for the exact signatures and adapt accordingly.

- [ ] **Step 2: Run integration tests**

Run: `cd 03_projects/yorkie && go test -tags integration ./test/integration/ -run TestCounterDedup -v`
Expected: all tests PASS (requires running MongoDB and Yorkie server)

- [ ] **Step 3: Commit**

```bash
git add test/integration/counter_dedup_test.go
git commit -m "Add integration tests for Counter dedup mode"
```

---

### Task 9: HyperLogLog Implementation (JS SDK)

**Files:**
- Create: `packages/sdk/src/document/crdt/hll.ts`

- [ ] **Step 1: Implement HLL in TypeScript**

Create `packages/sdk/src/document/crdt/hll.ts`:

```typescript
/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

const HLL_PRECISION = 14;
const HLL_REGISTER_COUNT = 1 << HLL_PRECISION;

/**
 * `HLL` is a HyperLogLog probabilistic cardinality estimator.
 * It supports add, count, merge (max-merge), and serialization.
 */
export class HLL {
  private registers: Uint8Array;

  constructor() {
    this.registers = new Uint8Array(HLL_REGISTER_COUNT);
  }

  /**
   * `add` inserts a value into the HLL. Returns true if the value changed a
   * register (likely new), false otherwise (likely duplicate).
   */
  public add(value: string): boolean {
    const hash = this.hash(value);
    const idx = Number(hash >> BigInt(64 - HLL_PRECISION));
    const remaining =
      (hash << BigInt(HLL_PRECISION)) |
      (1n << BigInt(HLL_PRECISION - 1));
    const rho = this.countLeadingZeros(remaining) + 1;

    if (rho > this.registers[idx]) {
      this.registers[idx] = rho;
      return true;
    }
    return false;
  }

  /**
   * `count` returns the estimated cardinality.
   */
  public count(): number {
    const m = HLL_REGISTER_COUNT;
    const alpha = 0.7213 / (1.0 + 1.079 / m);

    let sum = 0;
    let zeros = 0;
    for (let i = 0; i < m; i++) {
      sum += Math.pow(2, -this.registers[i]);
      if (this.registers[i] === 0) {
        zeros++;
      }
    }

    let estimate = alpha * m * m / sum;

    // Small range correction
    if (estimate <= 2.5 * m && zeros > 0) {
      estimate = m * Math.log(m / zeros);
    }

    return Math.round(estimate);
  }

  /**
   * `merge` performs max-merge with another HLL. This operation is
   * commutative, associative, and idempotent.
   */
  public merge(other: HLL): void {
    for (let i = 0; i < HLL_REGISTER_COUNT; i++) {
      if (other.registers[i] > this.registers[i]) {
        this.registers[i] = other.registers[i];
      }
    }
  }

  /**
   * `toBytes` returns the raw register array for serialization.
   */
  public toBytes(): Uint8Array {
    return new Uint8Array(this.registers);
  }

  /**
   * `restore` loads register data from bytes.
   */
  public restore(data: Uint8Array): void {
    this.registers.set(data);
  }

  /**
   * `hash` returns a 64-bit FNV-1a hash as BigInt.
   */
  private hash(value: string): bigint {
    let h = 0xcbf29ce484222325n;
    const prime = 0x100000001b3n;
    for (let i = 0; i < value.length; i++) {
      h ^= BigInt(value.charCodeAt(i));
      h = BigInt.asUintN(64, h * prime);
    }
    return h;
  }

  /**
   * `countLeadingZeros` counts leading zeros of a 64-bit BigInt.
   */
  private countLeadingZeros(x: bigint): number {
    if (x === 0n) return 64;
    let n = 0;
    while ((x & (1n << 63n)) === 0n) {
      n++;
      x <<= 1n;
    }
    return n;
  }
}
```

- [ ] **Step 2: Commit**

```bash
git add packages/sdk/src/document/crdt/hll.ts
git commit -m "Add HyperLogLog implementation for JS SDK"
```

---

### Task 10: Extend CRDT Counter with Dedup Mode (JS SDK)

**Files:**
- Modify: `packages/sdk/src/document/crdt/counter.ts`

- [ ] **Step 1: Add dedup fields and methods to CRDTCounter**

In `packages/sdk/src/document/crdt/counter.ts`, import the HLL class and add dedup support to `CRDTCounter`:

Add fields:
```typescript
private isDedup: boolean;
private hll?: HLL;
```

Add methods:
- `setDedup(dedup: boolean)`: Sets dedup mode and initializes HLL
- `getIsDedup(): boolean`: Returns dedup mode
- `increaseDedup(value: Primitive, actor: string)`: Adds to HLL, recomputes value if new
- `hllBytes(): Uint8Array | undefined`: Returns serialized HLL registers
- `restoreHLL(data: Uint8Array)`: Restores HLL from bytes and recomputes value

Update `deepcopy()` to copy HLL state when in dedup mode.

Update the static `create()` method to accept an optional `isDedup` parameter.

- [ ] **Step 2: Commit**

```bash
git add packages/sdk/src/document/crdt/counter.ts
git commit -m "Add dedup mode to CRDTCounter in JS SDK"
```

---

### Task 11: Extend JSON Counter Proxy and IncreaseOperation (JS SDK)

**Files:**
- Modify: `packages/sdk/src/document/json/counter.ts`
- Modify: `packages/sdk/src/document/operation/increase_operation.ts`

- [ ] **Step 1: Add actor field to IncreaseOperation**

In the IncreaseOperation class, add an optional `actor` field, an `Actor()` accessor, and an alternative constructor or optional parameter to `create()`.

- [ ] **Step 2: Add dedup increase to JSON Counter**

In `packages/sdk/src/document/json/counter.ts`, add an `increase` overload or a separate method that accepts `{ actor: string }` as a second parameter. When actor is provided:
- Call `this.counter.increaseDedup(value, actor)` instead of `this.counter.increase(value)`
- Push `IncreaseOperation` with the actor field set

- [ ] **Step 3: Commit**

```bash
git add packages/sdk/src/document/json/counter.ts packages/sdk/src/document/operation/increase_operation.ts
git commit -m "Add dedup increase to JSON Counter proxy and IncreaseOperation in JS SDK"
```

---

### Task 12: Update JS SDK Converter for Snapshot and Operation Serialization

**Files:**
- Modify: `packages/sdk/src/api/converter.ts`

- [ ] **Step 1: Update toCounter/fromCounter for snapshot HLL serialization**

In `converter.ts`, update the `toCounter()` function to include `isDedup` and `hllRegisters` fields when the CRDTCounter is in dedup mode.

Update `fromCounter()` to restore dedup state: when `pbCounter.isDedup` is true, call `counter.setDedup(true)` and `counter.restoreHLL(pbCounter.hllRegisters)`.

- [ ] **Step 2: Update toIncrease/fromIncrease for actor serialization**

Update the Increase operation serialization to include the `actor` field.

Update the deserialization to read the `actor` field and construct the operation accordingly.

- [ ] **Step 3: Build and test**

Run: `cd 03_projects/yorkie-js-sdk && pnpm sdk build && pnpm sdk test`
Expected: build succeeds, all existing tests PASS

- [ ] **Step 4: Commit**

```bash
git add packages/sdk/src/api/converter.ts
git commit -m "Serialize and deserialize dedup Counter state and actor in JS SDK converter"
```

---

### Task 13: Run Full Test Suite and Lint

**Files:** (none — verification only)

- [ ] **Step 1: Run Go server lint and tests**

Run: `cd 03_projects/yorkie && make lint && make test`
Expected: no lint errors, all unit tests PASS

- [ ] **Step 2: Run Go integration tests**

Run: `cd 03_projects/yorkie && make test -tags integration`
Expected: all integration tests PASS (requires running server)

- [ ] **Step 3: Run JS SDK lint and tests**

Run: `cd 03_projects/yorkie-js-sdk && pnpm lint && pnpm sdk build && pnpm sdk test`
Expected: no lint errors, build succeeds, all tests PASS

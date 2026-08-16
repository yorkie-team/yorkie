# Undo/Redo Go SDK Port Implementation Plan

**Created**: 2026-08-15

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port the JS SDK's undo/redo history layer to the Go SDK so both SDKs
produce identical reverse operations and converge identically.

**Architecture:** Thread a reverse operation out of `Operation.Execute` and
`Change.Execute`, widen the Text/Tree CRDT mutators so they return the state a
reverse operation needs, then build `History` and `Document.Undo/Redo` on top.
Ported phase by phase — Phase 0 changes signatures only and provably changes no
behavior; Phases 1–4 add reverse generation per data type.

**Tech Stack:** Go 1.24+, `github.com/stretchr/testify/assert`, build tag
`integration` for `test/integration/`, MongoDB for integration tests.

**Spec:** `docs/design/undo-redo-go-port.md` (read it first;
`docs/design/undo-redo.md` describes the algorithm being ported)

**Port source:** `yorkie-js-sdk` at v0.7.16 (commit `28a5a42e`). Every task
names the exact JS file and line range being ported. When JS and this plan
disagree, **JS wins** — the goal is parity, and the plan may have gone stale.

## Global Constraints

- Apache 2.0 license header on every new Go file (copy from any existing file,
  year `2026`).
- Every new package needs a package comment (`// Package xxx provides…`).
- Follow the [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md).
- `MaxUndoRedoStackDepth = 50` — same value as JS
  (`packages/sdk/src/document/history.ts:39`).
- Reverse operations accumulate in **reverse order** (JS uses
  `reverseOps.unshift`). Never append in forward order.
- An operation that cannot apply during undo/redo is **skipped, not an error**.
- Do not fix any defect JS still has. Overlapping-undo content duplication, Tree
  reconciliation Cases 3–6, `ArraySet` + `Move` undo, and GC vs. undo (#664)
  stay broken identically in both SDKs. Ported skipped tests stay skipped.
- Do not hand-edit `api/yorkie/v1/*.pb.go`. No proto changes are needed — the
  restore/retombstone wire format already exists.
- Commit message: subject ≤70 chars, blank line, body wrapped at 80.
- `make lint` green on every commit. `go test ./...` green on every commit;
  `make test` green whenever MongoDB is up.

## File Structure

**Created:**

| File | Responsibility |
|---|---|
| `pkg/document/history.go` | `History`, `HistoryOperation`, stack push/pop/clear, the three `Reconcile*` methods |
| `pkg/document/history_test.go` | Unit tests for stack depth, clearing, reconciliation |
| `test/integration/history_array_test.go` | Port of `history_array_test.ts` |
| `test/integration/history_text_test.go` | Port of `history_text_test.ts` |
| `test/integration/history_tree_test.go` | Port of `history_tree_test.ts` |
| `test/integration/history_tree_concurrent_test.go` | Port of `history_tree_concurrent_test.ts` |
| `test/integration/history_tree_split_test.go` | Port of `history_tree_split_test.ts` |

**Modified:**

| File | Change |
|---|---|
| `pkg/document/operations/operation.go` | `OpSource` type; `Execute` returns `(Operation, error)` and takes `source` |
| `pkg/document/operations/{add,array_set,edit,increase,move,remove,set,style,tree_edit,tree_style}.go` | New signature; reverse operation generation |
| `pkg/document/change/change.go` | `Change.Execute` returns `(executed, reverseOps, err)` |
| `pkg/document/change/context.go` | `previousPresence`, `reversePresenceKeys`, `ReversePresence()` |
| `pkg/document/document.go` | Collect reverse ops from `Update`; `Undo`/`Redo`/`CanUndo`/`CanRedo`/`ClearHistory`; `updating` guard; reconciliation in `applyChanges` |
| `pkg/document/internal_document.go` | `ApplyChanges` also returns executed operations |
| `pkg/document/presence/proxy.go` | `Set` takes variadic options; `WithHistory()` |
| `pkg/document/crdt/text.go` | `Edit` returns removed values and spans; `Style` returns previous attributes |
| `pkg/document/crdt/tree.go` | `Edit` returns removed contents and pre-tombstoned set; `Style`/`RemoveStyle` return previous attributes |
| `pkg/document/json/{text,tree}.go` | Absorb the widened CRDT return values |
| `client/client.go` | `ClearHistory` on attach |
| `test/integration/{object,counter}_test.go` | Undo cases from the JS counterparts |

Note: `test/integration/history_test.go` already exists and covers the admin
change-history API. It is unrelated — do not touch it.

---

## Phase 0 — Signatures and skeleton (no behavior change)

### Task 1: OpSource and the Execute signature

**Files:**
- Modify: `pkg/document/operations/operation.go`
- Modify: `pkg/document/operations/add.go:55`, `array_set.go:55`, `edit.go:110`,
  `increase.go:62`, `move.go:55`, `remove.go:51`, `set.go:56`, `style.go:83`,
  `tree_edit.go:109`, `tree_style.go:83`
- Modify: `pkg/document/change/change.go:54-66`
- Modify: `pkg/document/document.go:235`, `pkg/document/document.go:303`
- Modify: `pkg/document/internal_document.go:317`
- Test: existing suite

**Interfaces:**
- Produces: `operations.OpSource` with `OpSourceLocal`, `OpSourceRemote`,
  `OpSourceUndoRedo`; `Operation.Execute(root, source, vv) (Operation, error)`;
  `Change.Execute(root, presences, source) ([]operations.Operation, []operations.Operation, error)`
  returning `(executed, reverseOps, err)`.

- [ ] **Step 1: Add `OpSource` and change the interface**

In `pkg/document/operations/operation.go`:

```go
// OpSource represents the source of an operation execution. Some operations
// behave differently under undo/redo, where a Set or an Add acts as a
// replacement rather than an insertion.
type OpSource int

const (
	// OpSourceLocal is an operation executed by a local edit.
	OpSourceLocal OpSource = iota

	// OpSourceRemote is an operation received from another client.
	OpSourceRemote

	// OpSourceUndoRedo is an operation replayed from the undo/redo stack.
	OpSourceUndoRedo
)

// Operation represents an operation to be executed on a document.
type Operation interface {
	// Execute executes this operation on the given document(`root`) and
	// returns the reverse operation that undoes it. The reverse is nil when
	// this operation has none.
	Execute(root *crdt.Root, source OpSource, versionVector time.VersionVector) (Operation, error)

	// ExecutedAt returns execution time of this operation.
	ExecutedAt() *time.Ticket

	// SetActor sets the given actor to this operation.
	SetActor(id time.ActorID)

	// ParentCreatedAt returns the creation time of the target element to
	// execute the operation.
	ParentCreatedAt() *time.Ticket
}
```

- [ ] **Step 2: Update all ten operations to the new signature, returning `nil` reverse**

Mechanical. For each file, change the receiver method and every `return`. Example
for `pkg/document/operations/set.go:56`:

```go
func (o *Set) Execute(root *crdt.Root, _ OpSource, _ time.VersionVector) (Operation, error) {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*crdt.Object)
	if !ok {
		return nil, ErrNotApplicableDataType
	}

	value, err := o.value.DeepCopy()
	if err != nil {
		return nil, err
	}
	removed := obj.Set(o.key, value)
	root.RegisterElement(value)
	if removed != nil {
		root.RegisterRemovedElementPair(obj, removed)
	}
	if value.RemovedAt() != nil {
		root.RegisterRemovedElementPair(obj, value)
	}
	return nil, nil
}
```

Do not add any reverse generation in this task. Every operation returns
`nil` for the reverse.

- [ ] **Step 3: Update `Change.Execute`**

In `pkg/document/change/change.go`:

```go
// Execute applies this change to the given JSON root. It returns the
// operations that actually executed and the reverse operations that undo
// them, in reverse order.
func (c *Change) Execute(
	root *crdt.Root,
	presences *inner.Map,
	source operations.OpSource,
) ([]operations.Operation, []operations.Operation, error) {
	var executed []operations.Operation
	var reverseOps []operations.Operation

	for _, op := range c.operations {
		reverseOp, err := op.Execute(root, source, c.ID().versionVector)
		if err != nil {
			return nil, nil, err
		}
		executed = append(executed, op)

		// NOTE(hackerwins): Reverse operations are accumulated in reverse
		// order so that undoing a change replays its operations backwards.
		if reverseOp != nil {
			reverseOps = append([]operations.Operation{reverseOp}, reverseOps...)
		}
	}

	if c.presenceChange != nil {
		c.presenceChange.Execute(c.id.actorID, presences)
	}

	return executed, reverseOps, nil
}
```

- [ ] **Step 4: Update the three callers**

`pkg/document/document.go:235` (local `Update`, real root) — discard for now,
Task 3 wires it:

```go
if _, _, err := c.Execute(d.doc.root, d.doc.presences, operations.OpSourceLocal); err != nil {
	return err
}
```

`pkg/document/document.go:303` (`applyChanges`, clone):

```go
if _, _, err := c.Execute(d.cloneRoot, d.clonePresences, operations.OpSourceRemote); err != nil {
	return err
}
```

`pkg/document/internal_document.go:317`:

```go
if _, _, err := c.Execute(d.root, d.presences, operations.OpSourceRemote); err != nil {
	return nil, err
}
```

- [ ] **Step 5: Fix the test files that call `op.Execute` directly**

`pkg/document/crdt/text_restore_test.go` (lines 190, 197, 283, 342, 407, 421,
440, 483) and `pkg/document/crdt/tree_restore_test.go:212` call `op.Execute`
directly. Update each call:

```go
_, err := op.Execute(root, operations.OpSourceRemote, nil)
assert.NoError(t, err)
```

and for the error assertions:

```go
_, err := op.Execute(root, operations.OpSourceRemote, vv)
assert.ErrorIs(t, err, operations.ErrUnknownRestoreIdentity)
```

- [ ] **Step 6: Verify no behavior changed**

Run: `go test ./...`
Expected: PASS — every test that passed before still passes.

Run: `make lint`
Expected: clean.

Run: `make test` (requires MongoDB via
`docker compose -f build/docker/docker-compose.yml up -d`)
Expected: PASS.

This is the whole point of Phase 0: the signature moved, nothing else did.

- [ ] **Step 7: Commit**

```bash
git add pkg/document/operations pkg/document/change pkg/document/document.go \
  pkg/document/internal_document.go pkg/document/crdt/text_restore_test.go \
  pkg/document/crdt/tree_restore_test.go
git commit -m "Return a reverse operation from operation execution"
```

Body: explain that `OpSource` is needed because `Set` and `Remove` behave
differently under undo, that every operation returns `nil` for now, and that
this commit is behavior-neutral by construction.

---

### Task 2: History and the Document API skeleton

**Files:**
- Create: `pkg/document/history.go`
- Create: `pkg/document/history_test.go`
- Modify: `pkg/document/document.go`

**Interfaces:**
- Consumes: `operations.Operation` from Task 1.
- Produces: `document.MaxUndoRedoStackDepth`; `document.HistoryOperation`;
  `(*Document).Undo() error`, `.Redo() error`, `.CanUndo() bool`,
  `.CanRedo() bool`, `.ClearHistory()`; `document.ErrRefusedDuringUpdate`.

- [ ] **Step 1: Write the failing tests**

Create `pkg/document/history_test.go`:

```go
func TestHistoryStack(t *testing.T) {
	t.Run("empty stack undo is a no-op test", func(t *testing.T) {
		doc := document.New("d1")
		assert.False(t, doc.CanUndo())
		assert.False(t, doc.CanRedo())
		assert.NoError(t, doc.Undo())
		assert.NoError(t, doc.Redo())
	})

	t.Run("undo inside an updater is refused test", func(t *testing.T) {
		doc := document.New("d1")
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			return doc.Undo()
		})
		assert.ErrorIs(t, err, document.ErrRefusedDuringUpdate)
	})

	t.Run("stack depth is capped test", func(t *testing.T) {
		doc := document.New("d1")
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("c", crdt.LongCnt, 0)
			return nil
		}))
		for i := 0; i < document.MaxUndoRedoStackDepth+10; i++ {
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetCounter("c").Increase(1)
				return nil
			}))
		}
		assert.Equal(t, document.MaxUndoRedoStackDepth, doc.UndoStackLenForTest())
	})
}
```

The third test only passes once Task 4 generates `Increase` reverses. Mark it
`t.Skip("enabled in Task 4")` for now and remove the skip in Task 4.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/document/ -run TestHistoryStack -v`
Expected: FAIL — `doc.CanUndo` undefined.

- [ ] **Step 3: Write `pkg/document/history.go`**

```go
// MaxUndoRedoStackDepth is the maximum depth of the undo/redo stacks. It
// mirrors the JS SDK's MaxUndoRedoStackDepth.
const MaxUndoRedoStackDepth = 50

// HistoryOperation is a single entry in an undo/redo stack. Op is nil when
// this entry carries a presence change instead of an operation.
type HistoryOperation struct {
	Op       operations.Operation
	Presence presence.Data
}

// History stores the undo/redo stacks of a document. Each entry is the set of
// reverse operations produced by one change.
type History struct {
	undoStack [][]HistoryOperation
	redoStack [][]HistoryOperation
}

// NewHistory creates a new instance of History.
func NewHistory() *History {
	return &History{}
}

// HasUndo reports whether there is anything to undo.
func (h *History) HasUndo() bool { return len(h.undoStack) > 0 }

// HasRedo reports whether there is anything to redo.
func (h *History) HasRedo() bool { return len(h.redoStack) > 0 }

// PushUndo pushes the reverse operations of a change onto the undo stack,
// dropping the oldest entry when the stack is full.
func (h *History) PushUndo(ops []HistoryOperation) {
	if len(h.undoStack) >= MaxUndoRedoStackDepth {
		h.undoStack = h.undoStack[1:]
	}
	h.undoStack = append(h.undoStack, ops)
}

// PopUndo pops the most recent entry off the undo stack. It returns nil when
// the stack is empty.
func (h *History) PopUndo() []HistoryOperation {
	if len(h.undoStack) == 0 {
		return nil
	}
	ops := h.undoStack[len(h.undoStack)-1]
	h.undoStack = h.undoStack[:len(h.undoStack)-1]
	return ops
}

// PushRedo and PopRedo mirror PushUndo and PopUndo for the redo stack.
func (h *History) PushRedo(ops []HistoryOperation) { /* same shape as PushUndo */ }
func (h *History) PopRedo() []HistoryOperation     { /* same shape as PopUndo */ }

// ClearUndo empties the undo stack.
func (h *History) ClearUndo() { h.undoStack = nil }

// ClearRedo empties the redo stack.
func (h *History) ClearRedo() { h.redoStack = nil }
```

Write `PushRedo` and `PopRedo` out in full — the comment above is shorthand for
this plan only, not for the source file.

- [ ] **Step 4: Add the Document API**

In `pkg/document/document.go`, add `history *History` and
`updating atomic.Bool` to `Document`, initialize `history` in `New`, and set
`d.updating.Store(true)` at the top of `Update` with a deferred
`d.updating.Store(false)` — **before** `d.mu.Lock()`, so the flag is visible to
a re-entrant caller that has not yet blocked on the mutex.

```go
// ErrRefusedDuringUpdate occurs when Undo or Redo is called from inside an
// updater. The updater already holds the document lock, so proceeding would
// deadlock rather than fail.
var ErrRefusedDuringUpdate = errors.New("undo/redo is not allowed during an update")

// CanUndo reports whether there is a change to undo.
func (d *Document) CanUndo() bool {
	if d.updating.Load() {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.history.HasUndo()
}

// Undo reverses the last local change. It is a no-op when the undo stack is
// empty.
func (d *Document) Undo() error {
	return d.executeUndoRedo(true)
}

// Redo replays the last undone change. It is a no-op when the redo stack is
// empty.
func (d *Document) Redo() error {
	return d.executeUndoRedo(false)
}

// ClearHistory flushes both stacks. Changes made before this call are no
// longer reachable via undo.
func (d *Document) ClearHistory() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.history.ClearUndo()
	d.history.ClearRedo()
}
```

`executeUndoRedo` is the port of `document.ts:2049-2165`. Write it in full now —
it is correct even while every reverse is nil, because an empty popped entry
short-circuits:

```go
func (d *Document) executeUndoRedo(isUndo bool) error {
	if d.updating.Load() {
		return ErrRefusedDuringUpdate
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	var entries []HistoryOperation
	if isUndo {
		entries = d.history.PopUndo()
	} else {
		entries = d.history.PopRedo()
	}
	if len(entries) == 0 {
		return nil
	}

	if err := d.ensureClone(); err != nil {
		return err
	}

	ctx := change.NewContext(d.doc.changeID, "", d.cloneRoot)
	for _, entry := range entries {
		if entry.Op == nil {
			// Presence entries are applied in Task 7.
			continue
		}

		ticket := ctx.IssueTimeTicket()
		entry.Op.SetExecutedAt(ticket)
		// Ticket reissue hooks for ArraySet, Add and TreeEdit are added in
		// Tasks 6 and 15.
		ctx.Push(entry.Op)
	}

	c := ctx.ToChange()
	if _, _, err := c.Execute(d.cloneRoot, d.clonePresences, operations.OpSourceUndoRedo); err != nil {
		return err
	}
	executed, reverseOps, err := c.Execute(d.doc.root, d.doc.presences, operations.OpSourceUndoRedo)
	if err != nil {
		return err
	}

	var reverse []HistoryOperation
	for _, op := range reverseOps {
		reverse = append(reverse, HistoryOperation{Op: op})
	}
	if len(reverse) > 0 {
		if isUndo {
			d.history.PushRedo(reverse)
		} else {
			d.history.PushUndo(reverse)
		}
	}

	if len(executed) == 0 && c.PresenceChange() == nil {
		return nil
	}

	d.doc.localChanges = append(d.doc.localChanges, c)
	d.doc.changeID = ctx.NextID()
	return nil
}
```

`Operation.SetExecutedAt` does not exist yet — add it to the interface and to
all ten operations alongside the existing `SetActor`.

Add a test-only accessor:

```go
// UndoStackLenForTest returns the undo stack depth for testing.
func (d *Document) UndoStackLenForTest() int { return len(d.history.undoStack) }
```

- [ ] **Step 5: Run the tests**

Run: `go test ./pkg/document/ -run TestHistoryStack -v`
Expected: PASS (third subtest skipped).

Run: `go test ./...` and `make lint`
Expected: PASS, clean.

- [ ] **Step 6: Commit**

```bash
git add pkg/document/history.go pkg/document/history_test.go pkg/document/document.go
git commit -m "Add the undo/redo history layer to the Go SDK"
```

---

### Task 3: Collect reverse operations from local updates

**Files:**
- Modify: `pkg/document/document.go:235`
- Test: covered by Task 4

**Interfaces:**
- Consumes: `Change.Execute` reverse return from Task 1; `History` from Task 2.

- [ ] **Step 1: Wire the local `Update` path**

Replace the discard from Task 1 Step 4:

```go
executed, reverseOps, err := c.Execute(d.doc.root, d.doc.presences, operations.OpSourceLocal)
if err != nil {
	return err
}

var reverse []HistoryOperation
for _, op := range reverseOps {
	reverse = append(reverse, HistoryOperation{Op: op})
}
if len(reverse) > 0 {
	d.history.PushUndo(reverse)
}

// NOTE(hackerwins): A new local operation invalidates the redo stack.
if len(executed) > 0 {
	d.history.ClearRedo()
}
```

- [ ] **Step 2: Verify nothing regressed**

Run: `go test ./...` and `make lint`
Expected: PASS, clean. No reverses exist yet, so both stacks stay empty.

- [ ] **Step 3: Commit**

```bash
git add pkg/document/document.go
git commit -m "Push reverse operations of a local update onto the undo stack"
```

---

## Phase 1 — Object, Array, Counter

### Task 4: Counter reverse operation

**Files:**
- Modify: `pkg/document/operations/increase.go:62`
- Modify: `pkg/document/history_test.go` (remove the Task 2 skip)
- Test: `test/integration/counter_test.go`

**Port source:** `packages/sdk/src/document/operation/increase_operation.ts:95-130`

**Interfaces:**
- Produces: the first non-nil reverse operation, which proves the Task 2 and
  Task 3 plumbing end to end.

- [ ] **Step 1: Write the failing test**

In `test/integration/counter_test.go`, inside the existing test function:

```go
t.Run("counter undo/redo test", func(t *testing.T) {
	ctx := context.Background()
	doc := document.New(helper.TestKey(t))
	assert.NoError(t, cli.Attach(ctx, doc))
	defer func() { assert.NoError(t, cli.Detach(ctx, doc)) }()

	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewCounter("age", crdt.LongCnt, 10)
		return nil
	}))
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetCounter("age").Increase(5)
		return nil
	}))
	assert.Equal(t, `{"age":15}`, doc.Marshal())

	assert.NoError(t, doc.Undo())
	assert.Equal(t, `{"age":10}`, doc.Marshal())

	assert.NoError(t, doc.Redo())
	assert.Equal(t, `{"age":15}`, doc.Marshal())
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -tags integration ./test/integration/ -run TestCounter -v`
Expected: FAIL — undo leaves the value at 15.

- [ ] **Step 3: Generate the reverse**

In `pkg/document/operations/increase.go`, negate the value. Mirror JS exactly:
a dedup counter produces **no** reverse, because HyperLogLog cannot remove an
actor once added (`increase_operation.ts:109`).

```go
func (o *Increase) Execute(root *crdt.Root, _ OpSource, _ time.VersionVector) (Operation, error) {
	parent := root.FindByCreatedAt(o.parentCreatedAt)
	cnt, ok := parent.(*crdt.Counter)
	if !ok {
		return nil, ErrNotApplicableDataType
	}

	value, ok := o.value.(*crdt.Primitive)
	if !ok {
		return nil, ErrNotApplicableDataType
	}

	// Compute the reverse before mutating, mirroring the JS SDK.
	var reverseOp Operation
	if o.actor == nil {
		negated, err := negatePrimitive(value)
		if err != nil {
			return nil, err
		}
		reverseOp = NewIncrease(o.parentCreatedAt, negated, o.executedAt)
	}

	// ... existing increase / increaseDedup body, unchanged ...

	return reverseOp, nil
}
```

`negatePrimitive` deep-copies the primitive and negates it, handling both
`Long` (int64) and `Integer` (int32) types — see
`increase_operation.ts:118-129`. Put it in the same file, unexported.

If `Increase` has no `actor` field in Go, use whatever field marks the dedup
path (`IntegerDedupCnt` / `LongDedupCnt` counter type is the equivalent signal);
read `crdt.Counter` to confirm before writing the condition.

- [ ] **Step 4: Run the tests**

Run: `go test -tags integration ./test/integration/ -run TestCounter -v`
Expected: PASS.

- [ ] **Step 5: Enable the capped-stack test**

Remove the `t.Skip("enabled in Task 4")` from `pkg/document/history_test.go`.

Run: `go test ./pkg/document/ -run TestHistoryStack -v`
Expected: PASS.

- [ ] **Step 6: Add a dedup-counter guard test**

```go
t.Run("dedup counter has no undo test", func(t *testing.T) {
	// A dedup counter cannot be undone: HyperLogLog cannot remove an actor.
	// Assert the undo stack stays empty after an increase.
})
```

Write the body against the existing dedup counter tests in
`test/integration/counter_dedup_test.go` for the correct constructor.

- [ ] **Step 7: Commit**

```bash
git add pkg/document/operations/increase.go pkg/document/history_test.go \
  test/integration/counter_test.go
git commit -m "Generate the reverse operation for Counter.Increase"
```

---

### Task 5: Object Set and Remove reverse operations

**Files:**
- Modify: `pkg/document/operations/set.go:56`, `pkg/document/operations/remove.go:51`
- Test: `test/integration/object_test.go`

**Port source:** `set_operation.ts:70-142`, `remove_operation.ts:70-125`

**Interfaces:**
- Produces: reverse `Set` / `Remove` / `Add`; the removed-ancestor guard used by
  every later undo path.

- [ ] **Step 1: Write the failing tests**

In `test/integration/object_test.go`, port the undo cases from
`packages/sdk/test/integration/object_test.ts` — set over an existing key, set
over a new key, remove and undo, and undo after the parent was removed
concurrently. Use the JS assertions verbatim, translated to `doc.Marshal()`
comparisons.

- [ ] **Step 2: Run to verify they fail**

Run: `go test -tags integration ./test/integration/ -run TestObject -v`
Expected: FAIL.

- [ ] **Step 3: Implement the removed-ancestor guard**

Both operations skip execution during undo/redo when the target or any ancestor
is tombstoned (`set_operation.ts:81-89`, `remove_operation.ts:84-92`). Skipping
means returning `(nil, nil)` — not an error.

```go
// isRemovedOrOrphaned reports whether elem or any of its ancestors has been
// removed. During undo/redo an operation targeting such an element is
// skipped rather than executed.
func isRemovedOrOrphaned(root *crdt.Root, elem crdt.Element) bool {
	for elem != nil {
		if elem.RemovedAt() != nil {
			return true
		}
		pair := root.FindElementPairByCreatedAt(elem.CreatedAt())
		if pair == nil {
			return false
		}
		elem = pair.Parent
	}
	return false
}
```

Confirm the accessor names against `pkg/document/crdt/root.go` before writing —
`FindElementPairByCreatedAt` may be spelled differently in Go.

- [ ] **Step 4: Generate the `Set` reverse**

Capture the previous value **before** mutating (`set_operation.ts:91-92`):

```go
func (o *Set) Execute(root *crdt.Root, source OpSource, _ time.VersionVector) (Operation, error) {
	parent := root.FindByCreatedAt(o.parentCreatedAt)
	obj, ok := parent.(*crdt.Object)
	if !ok {
		return nil, ErrNotApplicableDataType
	}

	if source == OpSourceUndoRedo && isRemovedOrOrphaned(root, obj) {
		return nil, nil
	}

	// The reverse restores the previous value, or removes the key when there
	// was none.
	previous := obj.Get(o.key)
	var reverseOp Operation
	if previous != nil && previous.RemovedAt() == nil {
		copied, err := previous.DeepCopy()
		if err != nil {
			return nil, err
		}
		reverseOp = NewSet(o.parentCreatedAt, o.key, copied, o.executedAt)
	} else {
		reverseOp = NewRemove(o.parentCreatedAt, o.value.CreatedAt(), o.executedAt)
	}

	// ... existing body ...

	return reverseOp, nil
}
```

Also port `set_operation.ts:98-103`: during undo/redo, when the value being set
already exists in the root under its `createdAt`, deregister it before
re-registering. Go is garbage-collected, but the root's element index still
needs the deregister.

- [ ] **Step 5: Generate the `Remove` reverse**

Port `remove_operation.ts:125-` (`toReverseOperation`). For an Array parent the
reverse is an `Add` carrying the deleted element and the `prevCreatedAt`
returned by `Array.FindPrevCreatedAt`; for an Object parent it is a `Set`
carrying the deleted element under its key. Build it **before** `container.Delete`,
since both look the element up by `createdAt`.

- [ ] **Step 6: Run the tests**

Run: `go test -tags integration ./test/integration/ -run TestObject -v`
Expected: PASS.

Run: `go test ./...` and `make lint`
Expected: PASS, clean.

- [ ] **Step 7: Commit**

```bash
git add pkg/document/operations/set.go pkg/document/operations/remove.go \
  test/integration/object_test.go
git commit -m "Generate reverse operations for Object.Set and Remove"
```

---

### Task 6: Array reverse operations and createdAt reconciliation

**Files:**
- Modify: `pkg/document/operations/add.go:55`, `move.go:55`, `array_set.go:55`
- Modify: `pkg/document/history.go` (add `ReconcileCreatedAt`)
- Modify: `pkg/document/document.go` (call it from `Update` and `executeUndoRedo`)
- Test: `pkg/document/history_test.go`

**Port source:** `add_operation.ts:88-99`, `move_operation.ts:80-106`,
`array_set_operation.ts:76-110`, `history.ts:123-160`, `document.ts:744-752`
and `document.ts:2088-2104`

**Interfaces:**
- Produces: `(*History).ReconcileCreatedAt(prev, curr *time.Ticket)`.

- [ ] **Step 1: Write the failing test**

In `pkg/document/history_test.go`:

```go
t.Run("reconcile createdAt after array set test", func(t *testing.T) {
	// A Set replaces the element, giving it a new createdAt. Reverse
	// operations already on the stack still point at the old one and must
	// be rewritten, or a later undo targets a dead element.
	doc := document.New("d1")
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewArray("list").AddInteger(1, 2, 3)
		return nil
	}))
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetArray("list").SetInteger(0, 9)
		return nil
	}))
	assert.NoError(t, doc.Undo())
	assert.Equal(t, `{"list":[1,2,3]}`, doc.Marshal())
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/document/ -run TestHistoryStack -v`
Expected: FAIL.

- [ ] **Step 3: Add `ReconcileCreatedAt`**

Port `history.ts:123-160`. Scan both stacks; rewrite `createdAt` on `ArraySet`,
`Remove` and `Move`, and `prevCreatedAt` on `Add` and `Move`:

```go
// ReconcileCreatedAt rewrites stacked reverse operations that still point at
// an element's previous createdAt. An element replaced by Set or revived by
// Add receives a new createdAt, which would otherwise leave the stack
// targeting a dead element.
func (h *History) ReconcileCreatedAt(prev, curr *time.Ticket) {
	replace := func(stack [][]HistoryOperation) {
		// TODO(hackerwins): Optimize by indexing operations.
		for _, entries := range stack {
			for _, entry := range entries {
				switch op := entry.Op.(type) {
				case *operations.ArraySet:
					if op.CreatedAt().Compare(prev) == 0 {
						op.SetCreatedAt(curr)
					}
				case *operations.Remove:
					if op.CreatedAt().Compare(prev) == 0 {
						op.SetCreatedAt(curr)
					}
				case *operations.Move:
					if op.CreatedAt().Compare(prev) == 0 {
						op.SetCreatedAt(curr)
					}
					if op.PrevCreatedAt().Compare(prev) == 0 {
						op.SetPrevCreatedAt(curr)
					}
				case *operations.Add:
					if op.PrevCreatedAt().Compare(prev) == 0 {
						op.SetPrevCreatedAt(curr)
					}
				}
			}
		}
	}
	replace(h.undoStack)
	replace(h.redoStack)
}
```

Add the `SetCreatedAt` / `SetPrevCreatedAt` setters the switch needs.

- [ ] **Step 4: Generate the three reverses**

- `Add` → `Remove` of the added element (`add_operation.ts:94-98`)
- `Move` → `Move` back to the previous position, read via `Array.PosCreatedAt`
  before mutating (`move_operation.ts:80`)
- `ArraySet` → `ArraySet` restoring the previous value
  (`array_set_operation.ts:104-109`)

- [ ] **Step 5: Call `ReconcileCreatedAt` from both paths**

In `Update`, after executing the change, for every executed `ArraySet`
(`document.ts:744-752`):

```go
for _, op := range executed {
	if set, ok := op.(*operations.ArraySet); ok {
		d.history.ReconcileCreatedAt(set.CreatedAt(), set.Value().CreatedAt())
	}
}
```

In `executeUndoRedo`, when issuing a ticket for a stacked `ArraySet` or `Add`,
reissue the value's `createdAt` and reconcile (`document.ts:2088-2104`).

- [ ] **Step 6: Run the tests**

Run: `go test ./pkg/document/ -v` and `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/document/operations/add.go pkg/document/operations/move.go \
  pkg/document/operations/array_set.go pkg/document/history.go \
  pkg/document/document.go pkg/document/history_test.go
git commit -m "Generate Array reverse operations and reconcile createdAt"
```

---

### Task 7: Presence in the history

**Files:**
- Modify: `pkg/document/presence/proxy.go:83`
- Modify: `pkg/document/change/context.go`
- Modify: `pkg/document/document.go` (`executeUndoRedo` presence branch)
- Test: `test/integration/doc_presence_test.go`

**Port source:** `presence.ts:57-71,190-230`, `document.ts:2078-2086`

**Interfaces:**
- Produces: `presence.WithHistory()` option;
  `(*change.Context).ReversePresence() presence.Data`.

- [ ] **Step 1: Write the failing test**

Port the presence undo case from the JS suite: set a presence key with history
enabled, undo, assert the previous value is restored and that other clients see
it.

- [ ] **Step 2: Run to verify it fails**

Run: `go test -tags integration ./test/integration/ -run TestPresence -v`
Expected: FAIL.

- [ ] **Step 3: Add the variadic option**

```go
// SetOption configures a presence Set.
type SetOption func(*setConfig)

type setConfig struct{ addToHistory bool }

// WithHistory marks this presence change as undoable, so it is pushed onto
// the document's undo stack alongside operation reverses.
func WithHistory() SetOption {
	return func(c *setConfig) { c.addToHistory = true }
}

// Set sets the given key-value pair.
func (p *Presence) Set(key, value string, opts ...SetOption) {
	// ... existing body, plus: when addToHistory, record the key on the
	// context so ReversePresence can rebuild the previous value.
}
```

Variadic keeps every existing caller compiling unchanged.

- [ ] **Step 4: Track the previous presence on the context**

Add `previousPresence presence.Data` and `reversePresenceKeys map[string]struct{}`
to `change.Context`, populated from the clone's presence at context creation,
plus:

```go
// ReversePresence returns the presence values that undoing this change would
// restore, or nil when no presence key was marked undoable.
func (c *Context) ReversePresence() presence.Data {
	if len(c.reversePresenceKeys) == 0 {
		return nil
	}
	reverse := presence.NewData()
	for key := range c.reversePresenceKeys {
		reverse[key] = c.previousPresence[key]
	}
	return reverse
}
```

`change.NewContext` gains a presence argument; update its callers in
`document.go`.

- [ ] **Step 5: Push and apply presence entries**

In `Update` and in `executeUndoRedo`, append
`HistoryOperation{Presence: ctx.ReversePresence()}` to the reverse entries when
non-nil. In `executeUndoRedo`, replace the `entry.Op == nil` skip with applying
the presence via the context, marked `WithHistory()` so redo works.

- [ ] **Step 6: Run the tests**

Run: `go test -tags integration ./test/integration/ -run TestPresence -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/document/presence pkg/document/change/context.go \
  pkg/document/document.go test/integration/doc_presence_test.go
git commit -m "Support undoing presence changes"
```

---

### Task 8: Port history_array_test.ts and clear history on attach

**Files:**
- Create: `test/integration/history_array_test.go`
- Modify: `client/client.go`

**Port source:** `packages/sdk/test/integration/history_array_test.ts` (2 tests)

- [ ] **Step 1: Port both tests**

Translate the two tests verbatim — same document contents, same operation
sequence, same assertions. Use the file header and client setup from an
existing integration test such as `test/integration/array_test.go`, including
the `//go:build integration` tag.

- [ ] **Step 2: Run to verify they pass or fail honestly**

Run: `go test -tags integration ./test/integration/ -run TestHistoryArray -v`
Expected: PASS. A failure here is a genuine Phase 1 gap — fix the operation, not
the test.

- [ ] **Step 3: Clear history on attach**

`document.ts:1468` calls `clearHistory()` on attach so pre-attach changes are
not reachable via undo (#1238). Add the same call in `client.Attach`, after the
document reaches attached status.

- [ ] **Step 4: Write the attach test**

```go
t.Run("history is cleared on attach test", func(t *testing.T) {
	ctx := context.Background()
	doc := document.New(helper.TestKey(t))
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewCounter("c", crdt.LongCnt, 1)
		return nil
	}))
	assert.True(t, doc.CanUndo())
	assert.NoError(t, cli.Attach(ctx, doc))
	defer func() { assert.NoError(t, cli.Detach(ctx, doc)) }()
	assert.False(t, doc.CanUndo())
})
```

- [ ] **Step 5: Run the full suite**

Run: `go test ./...`, `make lint`, `make test`
Expected: PASS, clean, PASS.

- [ ] **Step 6: Commit**

```bash
git add test/integration/history_array_test.go client/client.go
git commit -m "Port the Array history tests and clear history on attach"
```

---

## Phase 2 — Text

### Task 9: Widen the Text CRDT return values

**Files:**
- Modify: `pkg/document/crdt/text.go:322` (`Edit`), `:396` (`Style`)
- Modify: `pkg/document/crdt/rga_tree_split.go` (`edit`)
- Modify: `pkg/document/json/text.go:79`, `:153`
- Modify: `pkg/document/operations/edit.go`, `style.go` (absorb, ignore for now)
- Test: `pkg/document/crdt/text_test.go`

**Port source:** JS `text.edit()` returns
`[changes, pairs, diff, , removedValues, removedSpans]`; Go returns four values.

**Interfaces:**
- Produces:

```go
func (t *Text) Edit(
	from, to *RGATreeSplitNodePos,
	content string,
	attributes map[string]string,
	executedAt *time.Ticket,
	versionVector time.VersionVector,
) (*RGATreeSplitNodePos, []GCPair, resource.DataSize, []string, []RestoreSpan, error)

func (t *Text) Style(
	from, to *RGATreeSplitNodePos,
	attributes map[string]string,
	executedAt *time.Ticket,
	versionVector time.VersionVector,
) ([]GCPair, resource.DataSize, []PrevAttrs, error)
```

Use the existing span type from the restore work in `text.go` rather than
inventing a new one — read `Text.Restore` first and reuse its span type. If it
is unexported, export it or add a small exported alias.

- [ ] **Step 1: Write the failing test**

```go
func TestTextEditReturnsRemoved(t *testing.T) {
	// Deleting a range must report the removed values and the spans that
	// identify them, which is what an undo needs to revive them by identity.
}
```

Assert that deleting `"456"` from `"0123456789"` reports the removed value and a
span whose identity matches the tombstoned node.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/document/crdt/ -run TestTextEditReturnsRemoved -v`
Expected: FAIL — `Edit` returns four values.

- [ ] **Step 3: Widen the return values**

Thread the removed values and spans out of `rgaTreeSplit.edit`, which already
walks the removed nodes to build GC pairs. Update every caller to absorb the new
values with `_` where unused.

- [ ] **Step 4: Run the tests**

Run: `go test ./pkg/document/...` and `make lint`
Expected: PASS, clean. Still no behavior change — only return values moved.

- [ ] **Step 5: Commit**

```bash
git add pkg/document/crdt pkg/document/json pkg/document/operations
git commit -m "Return removed values and spans from Text.Edit"
```

---

### Task 10: Text Edit reverse operation

**Files:**
- Modify: `pkg/document/operations/edit.go:110`
- Test: `pkg/document/text_undo_test.go` (create)

**Port source:** `edit_operation.ts:180-330` — both the restore-mode branch
(`:207-219`) and the ordinary branch (`:235-256`)

- [ ] **Step 1: Write the failing test**

Single-client insert, delete, and replace, each undone and redone, asserting the
text and its node identities. Port the identity assertions from
`packages/sdk/test/unit/document/undo_content_identity_test.ts`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/document/ -run TestTextUndo -v`
Expected: FAIL.

- [ ] **Step 3: Implement the restore-mode branch**

The reverse of a restore-mode edit **keeps the same span sets and flips the
direction** — it does not copy content (`edit_operation.ts:207-219`):

```go
// The reverse keeps the same span sets and flips the direction.
reverseOp := NewEdit(
	e.parentCreatedAt,
	e.from, e.to,
	"", nil,
	e.executedAt,
	true, // isUndoOp
	e.restoreSpans,
	flipRestoreMode(e.restoreMode),
	e.retombstoneSpans,
)
```

Match the existing `NewEdit` signature in `edit.go` rather than the shape above.

- [ ] **Step 4: Implement the ordinary branch**

Build the reverse from `removedValues` and `removedSpans` returned by Task 9,
anchored at `text.NormalizePos(e.from)` (`edit_operation.ts:235-239`). When the
operation is itself an undo, refine its positions first
(`edit_operation.ts:222-225`):

```go
if e.isUndoOp {
	e.from = text.RefinePos(e.from)
	e.to = text.RefinePos(e.to)
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./pkg/document/... -v` and `make lint`
Expected: PASS, clean.

- [ ] **Step 6: Commit**

```bash
git add pkg/document/operations/edit.go pkg/document/text_undo_test.go
git commit -m "Generate the reverse operation for Text.Edit"
```

---

### Task 11: Text Style reverse operation

**Files:**
- Modify: `pkg/document/operations/style.go:83`
- Test: `pkg/document/text_undo_test.go`

**Port source:** `style_operation.ts:170-215`

- [ ] **Step 1: Write the failing test** — style a range, undo, assert the
  previous attributes return; style over an unstyled range, undo, assert the
  attribute is removed.
- [ ] **Step 2: Run to verify it fails.** Run:
  `go test ./pkg/document/ -run TestTextUndo -v`
- [ ] **Step 3: Implement.** JS branches three ways: restore previous
  attributes, remove an attribute that did not exist before, or set it back.
  Use the previous attributes returned by Task 9.
- [ ] **Step 4: Run the tests.** Expected: PASS.
- [ ] **Step 5: Commit** — `Generate the reverse operation for Text.Style`

---

### Task 12: Text position reconciliation

**Files:**
- Modify: `pkg/document/operations/edit.go` (add `ReconcileOperation`, `NormalizePos`)
- Modify: `pkg/document/history.go` (add `ReconcileTextEdit`)
- Modify: `pkg/document/internal_document.go:304` (also return executed ops)
- Modify: `pkg/document/document.go:296` (`applyChanges`)

**Port source:** `edit_operation.ts:327-410` (`normalizePos`,
`reconcileOperation`, Cases 1–6), `history.ts:162-186`, `document.ts:1552-1566`

**Interfaces:**
- Produces: `(*History).ReconcileTextEdit(parent *time.Ticket, from, to, contentLen int)`;
  `(*operations.Edit).NormalizePos(root *crdt.Root) (int, int)`;
  `(*operations.Edit).ReconcileOperation(remoteFrom, remoteTo, contentLen int)`;
  `(*InternalDocument).ApplyChanges(...) ([]DocEvent, []operations.Operation, error)`.

- [ ] **Step 1: Write the failing test**

The worked example from `edit_operation.ts:363-367`: text `"0123456789"`, a
pending undo range of `[4,6)`, a remote edit deleting `[2,4)` and inserting
`"XY"`, after which the undo range must read `[2,4)`.

- [ ] **Step 2: Run to verify it fails.** Run:
  `go test -tags integration ./test/integration/ -run TestHistoryText -v`

- [ ] **Step 3: Port `ReconcileOperation`**

All six overlap cases from `edit_operation.ts:366-`. Return early when
`!e.isUndoOp` and when `remoteFrom > remoteTo`. Note the comment at `:371-379`:
`restoreSpans` addresses content by identity, so the span payload is never
rewritten — only `fromPos` / `toPos`, which still serve as the fallback anchor.

- [ ] **Step 4: Thread executed operations out of `ApplyChanges`**

`InternalDocument.ApplyChanges` currently returns `([]DocEvent, error)`. Add the
executed operations as a second return and update its callers.

- [ ] **Step 5: Call the reconciliation from `applyChanges`**

```go
events, executed, err := d.doc.ApplyChanges(changes...)
if err != nil {
	return err
}

for _, op := range executed {
	if edit, ok := op.(*operations.Edit); ok {
		from, to := edit.NormalizePos(d.doc.root)
		d.history.ReconcileTextEdit(edit.ParentCreatedAt(), from, to, len(edit.Content()))
	}
}
```

- [ ] **Step 6: Run the tests.** Expected: PASS.
- [ ] **Step 7: Commit** — `Reconcile stacked Text undo positions with remote edits`

---

### Task 13: Port history_text_test.ts

**Files:**
- Create: `test/integration/history_text_test.go`

**Port source:** `packages/sdk/test/integration/history_text_test.ts` — 29
tests, of which two are skipped (`:705` Case 3 correctness, `:742` Case 5
correctness)

- [ ] **Step 1: Port all 29 tests**

One `t.Run` per JS `it`, same names. Two clients where the JS test uses two.

- [ ] **Step 2: Port the two skips**

```go
t.Run("Case 3 correctness: both undo of overlapping deletes should restore original test", func(t *testing.T) {
	t.Skip("KNOWN: overlapping undo duplicates content; see docs/design/undo-redo.md")
	// Body ported anyway, so enabling the test later needs only the skip removed.
})
```

- [ ] **Step 3: Run.** Run:
  `go test -tags integration ./test/integration/ -run TestHistoryText -v`
  Expected: 27 PASS, 2 SKIP. Any other failure is a real Phase 2 gap — fix the
  implementation, never the assertion.

- [ ] **Step 4: Count check**

Run: `grep -c "t.Run(" test/integration/history_text_test.go`
Expected: 29 — matching `grep -c "it(" ` on the JS file.

- [ ] **Step 5: Commit** — `Port the Text history tests from the JS SDK`

---

## Phase 3 — Tree

### Task 14: Widen the Tree CRDT return values

**Files:**
- Modify: `pkg/document/crdt/tree.go:1420` (`Edit`), `:2172` (`Style`), `:2276` (`RemoveStyle`)
- Modify: `pkg/document/json/tree.go`, `pkg/document/operations/tree_edit.go`, `tree_style.go`

**Port source:** JS `CRDTTree.edit()` returns the pre-tombstoned set consumed by
`toReverseOperation` (#1239)

**Interfaces:**
- Produces:

```go
func (t *Tree) Edit(
	from, to *TreePos,
	contents []*TreeNode,
	splitLevel int,
	editedAt *time.Ticket,
	issueTimeTicket func() *time.Ticket,
	versionVector time.VersionVector,
) ([]GCPair, resource.DataSize, []*TreeNode, map[string]struct{}, error)
```

returning removed contents and the pre-tombstoned node IDs.

- [ ] **Step 1: Write the failing test** — deleting a subtree reports the removed
  contents, and descendants already tombstoned before the edit appear in the
  pre-tombstoned set rather than in the removed contents.
- [ ] **Step 2: Run to verify it fails.** Run: `go test ./pkg/document/crdt/ -v`
- [ ] **Step 3: Widen the return values,** absorbing with `_` at every caller.
- [ ] **Step 4: Run `go test ./...` and `make lint`.** Expected: PASS, clean —
  no behavior change.
- [ ] **Step 5: Commit** — `Return removed contents and pre-tombstoned nodes from Tree.Edit`

---

### Task 15: Tree Edit reverse operation (splitLevel 0)

**Files:**
- Modify: `pkg/document/operations/tree_edit.go:109`
- Test: `pkg/document/tree_undo_test.go` (create)

**Port source:** `tree_edit_operation.ts:360-380` (restore branch),
`:466-510` (ordinary branch), `:80-95` (pre-tombstoned filtering)

**Interfaces:**
- Produces: `(*operations.TreeEdit).ReissueContentIDs(issue func() *time.Ticket)`.

- [ ] **Step 1: Write the failing test** — insert, delete, and replace on a tree,
  each undone and redone; plus the nested case from #1239 (typing inside a node
  that is later deleted, then undo/redo cycled twice) which must not accumulate
  duplicated content.
- [ ] **Step 2: Run to verify it fails.**
- [ ] **Step 3: Implement the restore branch** — same span sets, flipped
  direction, exactly as Task 10 did for Text.
- [ ] **Step 4: Implement the ordinary branch** — build `reverseContents` from
  the removed contents returned by Task 14, dropping any node in the
  pre-tombstoned set (`tree_edit_operation.ts:87`).
- [ ] **Step 5: Implement `ReissueContentIDs`**

A copy-reinserting reverse carries the original node IDs; inserting them again
puts two nodes under one ID (#1319). Restore-mode reverses revive by identity
and must keep theirs, so only the copy-reinsert path reissues. Call it from
`executeUndoRedo` before pushing the operation, replacing the Task 2 placeholder
comment (`document.ts:2105-2110`).

- [ ] **Step 6: Run the tests.** Expected: PASS.
- [ ] **Step 7: Commit** — `Generate the reverse operation for Tree.Edit`

---

### Task 16: Tree Style reverse operation

**Files:**
- Modify: `pkg/document/operations/tree_style.go:83`

**Port source:** `tree_style_operation.ts:160-210` (PR #1221)

- [ ] **Step 1: Write the failing test** — `setStyle` and `removeStyle`, each
  undone and redone.
- [ ] **Step 2: Run to verify it fails.**
- [ ] **Step 3: Implement** the same three-way branch as Task 11, using the
  previous attributes from Task 14.
- [ ] **Step 4: Run the tests.** Expected: PASS.
- [ ] **Step 5: Commit** — `Generate the reverse operation for Tree.Style`

---

### Task 17: Tree position reconciliation

**Files:**
- Modify: `pkg/document/operations/tree_edit.go` (`NormalizePos`, `ReconcileOperation`)
- Modify: `pkg/document/history.go` (`ReconcileTreeEdit`)
- Modify: `pkg/document/document.go` (`applyChanges`)

**Port source:** `tree_edit_operation.ts:715-780`, `history.ts:188-215`,
`document.ts:1567-1575`

**Interfaces:**
- Produces: `(*History).ReconcileTreeEdit(parent *time.Ticket, from, to, contentSize int)`.

- [ ] **Step 1: Write the failing test** — the non-overlapping concurrent cases
  (1, 2, 7: remote edit left of, right of, and adjacent to the pending undo
  range).
- [ ] **Step 2: Run to verify it fails.**
- [ ] **Step 3: Implement** — same overlap logic as `Edit.ReconcileOperation`,
  over integer indices.
- [ ] **Step 4: Extend the `applyChanges` loop** from Task 12 with a
  `*operations.TreeEdit` branch.
- [ ] **Step 5: Run the tests.** Expected: PASS.
- [ ] **Step 6: Commit** — `Reconcile stacked Tree undo positions with remote edits`

---

### Task 18: Port history_tree_test.ts and history_tree_concurrent_test.ts

**Files:**
- Create: `test/integration/history_tree_test.go` (32 tests)
- Create: `test/integration/history_tree_concurrent_test.go` (4 tests, 2 skipped)

**Port source:** the two JS files of the same name. The skips are at
`history_tree_concurrent_test.ts:143` and `:186`.

- [ ] **Step 1: Port all 32 single-client and 4 concurrent tests.**
- [ ] **Step 2: Port the two skips** with their `KNOWN:` reasons intact.
- [ ] **Step 3: Run.** Run:
  `go test -tags integration ./test/integration/ -run 'TestHistoryTree' -v`
  Expected: 34 PASS, 2 SKIP.
- [ ] **Step 4: Count check** — 32 and 4 `t.Run(` calls respectively.
- [ ] **Step 5: Commit** — `Port the Tree history tests from the JS SDK`

---

## Phase 4 — Tree split

### Task 19: Split-aware reverse operations

**Files:**
- Modify: `pkg/document/operations/tree_edit.go`

**Port source:** `tree_edit_operation.ts:486` (`toSplitReverseOperation`) and
`docs/design/tree-split-undo-redo.md`

- [ ] **Step 1: Write the failing test** — `splitLevel` 1 and 2 edits, each
  undone and redone, asserting the tree structure round-trips.
- [ ] **Step 2: Run to verify it fails.**
- [ ] **Step 3: Implement `toSplitReverseOperation`** — a boundary-deletion
  reverse built from `preEditFromIdx`, captured before the edit. Read
  `docs/design/tree-split-undo-redo.md` before writing; the boundary rules are
  documented there and not re-derivable from the JS code alone.
- [ ] **Step 4: Run the tests.** Expected: PASS.
- [ ] **Step 5: Commit** — `Generate boundary-deletion reverse ops for tree splits`

---

### Task 20: Port history_tree_split_test.ts

**Files:**
- Create: `test/integration/history_tree_split_test.go`

The "26 tests" this plan was written against is superseded. The ported file
holds 27 `t.Run(` call sites, several of which are table-driven, and they
expand to **79 runtime subtests, one of them skipped** — the audited figure,
measured with the command in Step 2.

- [x] **Step 1: Port the tests.**
- [x] **Step 2: Run.** Run:
  `go test -tags integration ./test/integration/ -run TestHistoryTreeSplit -v`
  Expected: 79 subtests, 78 PASS and 1 SKIP.
- [x] **Step 3: Count check** — 79 runtime subtests.
- [x] **Step 4: Commit** — `Port the Tree split history tests from the JS SDK`

---

## Wrap-up

### Task 21: Parity audit and documentation

**Files:**
- Modify: `docs/design/undo-redo.md` (Current Status)
- Modify: `docs/design/undo-redo-go-port.md`
- Create: `docs/tasks/active/20260815-undo-redo-go-port-lessons.md`

- [ ] **Step 1: Audit test-count parity**

For each JS history test file, compare the `it(` count against the `t.Run(`
count in its Go counterpart. Any JS test with no Go counterpart is a gap — port
it. Record the final counts in the lessons file.

- [ ] **Step 2: Audit the unit tests**

`undo_copy_path_test.ts`, `undo_content_identity_test.ts`,
`text_restore_after_gc_test.ts`, `text_restore_convergence_test.ts`,
`restore_converter_test.ts`, `tree_restore_converter_test.ts`,
`tree_duplicate_id_test.ts`. Some already have Go counterparts under
`pkg/document/crdt/`. Port the ones that do not.

- [ ] **Step 3: Update the design docs**

Add a Go column to the Current Status table in `docs/design/undo-redo.md`, and
record in `undo-redo-go-port.md` what actually shipped versus what this plan
predicted — particularly any signature that differs from the plan.

- [ ] **Step 4: Write the lessons file**

Capture, at minimum: whether the Phase 0 behavior-neutrality claim held, which
JS behaviors were not obvious from the code and needed a test to discover, and
any place where Go's semantics forced a divergence from JS.

- [ ] **Step 5: Full verification**

Run: `go test ./...`
Run: `make lint`
Run: `make test`
Run: `make test-complex`
Expected: all PASS, lint clean.

- [ ] **Step 6: Self review**

Dispatch `superpowers:requesting-code-review` (or `/code-review`) over the full
branch diff. Apply blocking findings; record non-blocking ones as known
limitations in the lessons file.

- [ ] **Step 7: Commit and open the PR**

```bash
git add docs/
git commit -m "Record the undo/redo Go port status and lessons"
git fetch && git rebase origin/main
```

PR title ≤70 chars. Body: Summary + Test plan, and an explicit note that the
commits are stacked in phase order and **must not be squashed** — Phase 0's
behavior-neutrality is only auditable as its own commit.

---

## Review

(Fill in after implementation.)

---
title: undo-redo-go-port
target-version: 0.7.17
---

# Undo/Redo Port to the Go SDK

## Problem

Undo/redo exists only in the JS SDK. The Go SDK has no history layer at all: no
undo/redo stacks, no reverse operation generation, and no position
reconciliation when remote changes arrive.

The gap is not symmetric. The *server-side* half of undo/redo is already in Go —
`CRDTText.Restore` / `Retombstone`, `CRDTTree.Restore` / `Retombstone`, the
`restoreMode` / `restoreSpans` / `retombstoneSpans` fields on `Edit` and
`TreeEdit`, and the wire format for all of it (#1875, #1893, #1913, #1924).
Go can *execute* the reverse operations a JS client sends. It cannot *produce*
them.

That asymmetry is the problem this document addresses. Every undo/redo fix
currently lands in JS and is mirrored into Go only as far as execution requires,
so the two implementations drift and behavioral divergence is invisible until a
document breaks in production.

### Goals

- Port the JS SDK history layer to the Go SDK so both SDKs behave identically.
- Port the JS undo/redo test suite to Go 1:1, including the tests JS currently
  skips, so divergence surfaces as a test failure rather than as a broken
  document.
- Keep the port behavior-identical to JS, including JS's known defects. Fixing a
  defect in Go alone would widen the gap the port exists to close.

### Non-Goals

- Server-side undo/redo or global history browsing (unchanged from
  [undo-redo.md](undo-redo.md)).
- Fixing the open items listed in [undo-redo.md](undo-redo.md) — overlapping
  undo content duplication, Tree reconciliation Cases 3–6, `ArraySet` + `Move`
  undo, GC vs. undo (#664). These stay broken identically in both SDKs.
- Emitting local change events from `Undo` / `Redo`. The Go SDK's `Update` does
  not emit local events today; that pre-existing difference is out of scope.
- Fixing defects found while porting Task 7 (presence in the history) that
  turned out to be pre-existing and cross-SDK: see the new rows in "Risks and
  Mitigation" below and
  [20260816-remote-redo-replica-divergence-todo.md](../tasks/active/20260816-remote-redo-replica-divergence-todo.md).
  **Plainly: redo does not propagate correctly to peers in either SDK
  today**, for any operation whose redo path reuses an original `createdAt`
  rather than re-ticketing it.

## Design

The two implementations are structurally symmetric, which is what makes a 1:1
port feasible:

| | JS | Go |
|---|---|---|
| Proxy | applies to clone, then `ctx.push(op)` | same (`p.context.Push`) |
| Apply | `change.execute(this.root, ...)` → `{opInfos, operations, reverseOps}` | `c.Execute(d.doc.root, ...)` → `error` |
| History | `Document.internalHistory` | **missing** |

Reverse operations are produced at the same point in both: the moment a change
is applied to the real root. The port therefore threads a return value through
two layers — operations and CRDT — and builds the history layer on top.

### Layer 1: Operation interface

JS's `operation.execute(root, source, versionVector)` takes an `OpSource`.
`SetOperation` and `RemoveOperation` branch on `source === OpSource.UndoRedo`,
because during undo a `Set` or an `Add` acts as a replacement rather than an
insertion. Go has no `OpSource` concept, so the signature changes on both sides:

```go
// pkg/document/operations/operation.go
type OpSource int

const (
	OpSourceLocal OpSource = iota
	OpSourceRemote
	OpSourceUndoRedo
)

type Operation interface {
	// Execute returns the reverse operation alongside the error. The reverse
	// is nil when the operation has none.
	Execute(root *crdt.Root, source OpSource, vv time.VersionVector) (Operation, error)
	ExecutedAt() *time.Ticket
	SetActor(id time.ActorID)
	ParentCreatedAt() *time.Ticket
}
```

```go
// pkg/document/change/change.go
func (c *Change) Execute(
	root *crdt.Root,
	presences *inner.Map,
	source operations.OpSource,
) (executed []operations.Operation, reverseOps []operations.Operation, err error)
```

The blast radius is small. `op.Execute` has exactly one production caller
(`change.go:56`), and `Change.Execute` has three:

| Caller | Role | Uses |
|---|---|---|
| `document.go:235` | local `Update`, real root | `reverseOps` → undo stack |
| `document.go:303` | `applyChanges`, clone | discards both |
| `internal_document.go:317` | remote changes, real root | `executed` → reconciliation |

Server code only calls `Change.Execute`, so the added return values are ignored
with `_` and the added `source` argument is always `OpSourceRemote`.

Two behaviors must match JS exactly:

1. **Reverse operations accumulate in reverse order.** JS uses
   `reverseOps.unshift(reverseOp)`. Appending in forward order breaks undo of
   chained operations within a single change.
2. **A non-applicable operation is skipped, not an error.** When JS's
   `execute` returns `undefined` — the target element was removed while
   executing undo/redo — the operation is skipped and omitted from the executed
   list. Go must mirror this rather than propagating an error.

### Layer 2: CRDT return values

Producing a reverse operation requires the state immediately before the
mutation. Go's CRDT mutators discard it. JS's `text.edit()` returns six values
(`[changes, pairs, diff, , removedValues, removedSpans]`); Go's returns four.

| Operation | Reverse | Prior state needed | Go today |
|---|---|---|---|
| `Set` (Object) | `Remove` or `Set` | previous value | `Object.Set` returns it ✅ |
| `Remove` | `Add` or `Set` | removed element, `prevCreatedAt` | `Array.Delete`, `FindPrevCreatedAt` ✅ |
| `Add` | `Remove` | none | ✅ |
| `Move` | `Move` | previous `prevCreatedAt` | `Array.PosCreatedAt` ✅ |
| `ArraySet` | `ArraySet` | previous value | discarded in `Execute` — return it ⚠️ |
| `Increase` | `Increase(-v)` | none | ✅ (no reverse for dedup counters) |
| `Edit` (Text) | `Edit` | `removedValues`, `removedSpans` | `Text.Edit` does not return them ❌ |
| `Style` (Text) | `Style` | previous attributes | `Text.Style` does not return them ❌ |
| `TreeEdit` (`splitLevel` 0) | `TreeEdit` | removed contents, `preTombstoned` | `Tree.Edit` does not return them ❌ |
| `TreeEdit` (`splitLevel` >= 1) | boundary-deletion `TreeEdit` | `preEditFromIdx` | ❌ |
| `TreeStyle` | `TreeStyle` | previous attributes | `Tree.Style` / `RemoveStyle` ❌ |

Object, Array, and Counter already have everything they need. Text and Tree
need widened return values on `Edit`, `Style`, and `RemoveStyle`. These methods
are on the server path too, but every change is an *added* return value that
existing callers discard, so the semantic risk is low even though the mechanical
reach is wide.

The reverse operation *payload* types already exist in Go — `RestoreMode`,
`restoreSpans`, and `retombstoneSpans` on `operations.Edit` and
`operations.TreeEdit`, with the wire format complete. Only the producer is
missing.

Rules that must match JS:

- An `Edit` reverse **keeps the same span sets and flips the direction**
  (restore ↔ retombstone). It does not deep-copy content and re-insert it. This
  is the identity-preserving restore introduced in #1875/#1893 and hardened
  through v0.7.16.
- An undo operation refines its positions before executing
  (`text.refinePos(this.fromPos)`).
- `TreeEdit` undo calls `reissueContentIDs()` before execution. A
  copy-reinserting reverse still carries the original node IDs; inserting them
  again puts two nodes under one ID (#1319). Restore-mode reverses revive by
  identity and must keep their IDs, so the two paths diverge here.
- Dedup counters produce no reverse operation. HyperLogLog cannot remove an
  actor once added.

### Layer 3: History

```go
// pkg/document/history.go (package document)

// MaxUndoRedoStackDepth mirrors the JS SDK.
const MaxUndoRedoStackDepth = 50

// HistoryOperation is one entry in an undo/redo stack. Op is nil for a
// presence entry.
type HistoryOperation struct {
	Op       operations.Operation
	Presence presence.Data
}

type History struct {
	undoStack [][]HistoryOperation
	redoStack [][]HistoryOperation
}
```

Go has no union types. A struct with a nil-discriminated `Op` reads closer to
JS's `Operation | {type: 'presence'}` than an interface with two implementations
would, and keeps the stack a plain slice.

Public API on `Document`:

```go
func (d *Document) Undo() error
func (d *Document) Redo() error
func (d *Document) CanUndo() bool
func (d *Document) CanRedo() bool
func (d *Document) ClearHistory()
```

`Undo` on an empty stack is a no-op returning nil, matching #1238. The redo
stack is cleared on any new local operation. `ClearHistory` is called on attach
(#1238); the Go client has no such hook today, so one is added to
`client.Attach`.

`Undo` follows the same sequence as JS's `executeUndoRedo`: pop the stack,
`ensureClone`, build a `change.Context`, then per operation issue a ticket and
apply `reconcileCreatedAt` (for `ArraySet` and `Add`) or `reissueContentIDs`
(for `TreeEdit`), execute the change against the clone, execute it against the
real root while collecting reverse operations, push those onto the opposite
stack, and skip appending to `localChanges` when nothing executed.

### Layer 4: Presence in history

JS marks a presence change as undoable with
`presence.set(value, { addToHistory: true })`, and `ChangeContext` tracks
`previousPresence` and `reversePresenceKeys` to build `getReversePresence()`.
Go's `presence.Presence.Set(key, value string)` has none of this. Additions:

- `Presence.Set(key, value string, opts ...SetOption)` — variadic, so existing
  callers are untouched.
- `previousPresence`, `reversePresenceKeys`, and `ReversePresence()` on
  `change.Context`.

### Layer 5: Reconciliation

A remote change shifts the positions that pending undo operations refer to. JS
walks the operations that actually executed against the real root and applies
three corrections:

| Hook | Target | Trigger |
|---|---|---|
| `reconcileCreatedAt(prev, curr)` | `createdAt` on `ArraySet` / `Remove` / `Move`, `prevCreatedAt` on `Add` / `Move` | local `ArraySet`; `ArraySet` and `Add` receiving a new ticket during undo/redo |
| `reconcileTextEdit(parent, from, to, len)` | `fromPos` / `toPos` of stacked `Edit` operations | remote `Edit` |
| `reconcileTreeEdit(parent, from, to, size)` | indices of stacked `TreeEdit` operations | remote `TreeEdit` |

In Go this hangs off `Document.applyChanges`, which calls
`d.doc.ApplyChanges(changes...)`. That method returns only events today, so it
must also return the executed operations.

The overlap logic itself (`reconcileOperation`, Cases 1–6) ports directly from
`edit_operation.ts` and `tree_edit_operation.ts`.

### Sequencing

A single PR, with commits stacked in this order and not squashed:

| Phase | Content | Done when |
|---|---|---|
| 0 | `OpSource`, `Operation.Execute` and `Change.Execute` signatures, `change.Context` reverse collection, `History`, `Document.Undo/Redo/CanUndo/CanRedo/ClearHistory` | every reverse is nil, **no behavior change**, existing tests green |
| 1 | Object / Array / Counter reverse operations, presence reverse | `history_array_test.go`, object and counter undo cases |
| 2 | Text `Edit` / `Style` reverse, `reconcileTextEdit` | `history_text_test.go` (73 runtime instances) |
| 3 | Tree `TreeEdit` (`splitLevel` 0) / `TreeStyle` reverse, `reconcileTreeEdit` | `history_tree_test.go` (135 runtime instances), `history_tree_concurrent_test.go` (14 runtime instances, 2 skipped) |
| 4 | Tree split `splitLevel` >= 1 | `history_tree_split_test.go` (26, 1 skipped) |

Phase 0 isolates the widest and riskiest change — the interface signatures —
into a step that provably changes nothing.

### Testing

The JS suite ports file for file. The existing
`test/integration/history_test.go` covers the admin change-history API and is
unrelated, so there is no name collision.

| JS | Go | Runtime instances |
|---|---|---|
| `history_array_test.ts` | `test/integration/history_array_test.go` | 84 (4 skipped) |
| `history_text_test.ts` | `test/integration/history_text_test.go` | 73 |
| `history_tree_test.ts` | `test/integration/history_tree_test.go` | 135 |
| `history_tree_concurrent_test.ts` | `test/integration/history_tree_concurrent_test.go` | 14 (2 skipped) |
| `history_tree_split_test.ts` | `test/integration/history_tree_split_test.go` | 26 (1 skipped) |
| undo cases in `object_test.ts`, `counter_test.ts` | corresponding Go files | — |
| `undo_copy_path_test.ts`, `undo_content_identity_test.ts`, and other unit tests | under `pkg/document/` | — |

**Counting method.** These files build many of their cases through
parameterized loops (`for (const op of ops) { it(...) }`, nested Cartesian
products), so a source-level count of `it(` calls badly understates what
actually runs — e.g. `history_array_test.ts` is 2 `it(` call sites but 84
runtime instances; `history_tree_test.ts` is 32 by the same grep but 135.
The counts above are **runtime instance counts**: derived by reading each
loop's structure and multiplying it out, then confirmed against the actual
`go test -v` output of the ported Go suite, which reports one
`--- PASS`/`--- SKIP` line per instance. `history_array_test.ts` and
`history_text_test.ts` were verified this way while porting them (Tasks 8
and 13, `.superpowers/sdd/20260815-undo-redo-go-port-todo/task-8-report.md`
and `task-13-report.md`); `history_tree_test.ts` and
`history_tree_concurrent_test.ts` while porting them (Task 18,
`task-18-report.md`). This is the third task in this port where a
grep-derived `it(` count misled the task brief — treat any count in this
document that was not independently re-derived as suspect until it is.

Five tests are currently skipped in JS as of `fa6cc513` (2026-08-09), all
carried over verbatim as `t.Skip("KNOWN: ...")` / `t.Skip("...")` with the
same reason, not fixed in Go:

- `history_text_test.ts:705`, `:742` — "Case 3/5 correctness" (overlapping
  undo content duplication; see `undo-redo.md`'s "Analysis: Overlapping Undo
  Content Duplication").
- `history_tree_concurrent_test.ts:143`, `:186` — "KNOWN: delete a whole
  `<p>` vs edit text inside it, both undo" and "KNOWN: delete two `<p>` vs
  edit inside first, both undo (segmentation)". Confirmed still skipped as of
  `fa6cc513` — unaffected by that commit, which fixed a different case (see
  `undo-redo.md`'s Tree reconciliation Cases 3-6 note).
- `history_tree_split_test.ts:800-802` — the `split-l2 → split-l2` undo
  chain, `TODO(#1235)`: "the boundary-deletion reverse op doesn't correctly
  restore the state when two consecutive L2 splits produce tombstoned
  structure."

**Do not un-skip any of these unilaterally when porting.** Test count is the
divergence check: a test present in JS and absent in Go is a gap; a test
skipped in JS and un-skipped in Go without independently confirming and
fixing the same defect in JS first is a port that has silently diverged.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Widened CRDT return values reach server code | Every change is an added return value; servers discard with `_`. Phase 0 proves zero behavior change first |
| Calling `Undo` inside an updater deadlocks instead of erroring — `Update` holds `d.mu`, whereas JS throws `ErrRefused` | An `updating atomic.Bool` set on entry to `Update`, checked by `Undo` / `Redo` **before** acquiring the lock, returning `ErrRefusedDuringUpdate` |
| Single PR is large (4,000+ lines of tests alone) | Commits stacked in phase order, never squashed |
| GC purging elements the undo stack still references (#664) | Left broken identically in both SDKs |
| Overlapping undo duplicates content | Reproduced identically. Fixing it in Go alone would widen the gap |
| Dedup counters | No reverse operation generated |
| A remotely-applied redo can permanently diverge a peer: a redone `Set` reuses its original `createdAt`, the `OpSourceUndoRedo`-gated stale-entry deregister at `set.go:102-104` doesn't fire under `OpSourceRemote`, and the peer's next GC pass purges the live restored element by that reused identity. Confirmed identical in JS (`set_operation.ts:99-104`), found during Task 7 | Left broken identically in both SDKs; filed as [20260816-remote-redo-replica-divergence-todo.md](../tasks/active/20260816-remote-redo-replica-divergence-todo.md). Fixing it in Go alone would widen the gap |
| `Presence.Initialize` (attach-time presence) leaves `Document.clonePresences` stale, so a later `Update`'s `Set` on a different key drops the attach-time key. Found during Task 7; the Go twin of JS's tracked `#608` (`document.ts:2068-2069`) | Worked around in Task 7's tests rather than fixed; filed in the same document above |
| Undoing a newly-introduced presence key: Go's `ReversePresence` sends the zero value (`""`) for a key absent from the snapshot; JS's `undefined` is dropped by `JSON.stringify` before the wire, removing the key instead. A genuine Go/JS divergence, not yet reconciled | Pinned, not fixed, by a characterization test in `pkg/document/history_test.go`; filed in the same document above |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Reverse operation returned from `Execute` | Matches where JS produces it — against the real root, after the mutation. Any other seam would need the prior state captured twice |
| `OpSource` added to `Execute` | `Set` and `Remove` genuinely behave differently under undo. Omitting it would force a hidden flag on the operation |
| `Document` methods rather than a `doc.History()` object | Idiomatic Go, and keeps mutex handling in one place. The JS shape is a JS convention, not behavior |
| `HistoryOperation` as a nil-discriminated struct | Closest readable analogue to JS's union; an interface with two implementations adds ceremony for no gain |
| Port JS's known defects as-is | The goal is parity. A one-sided fix widens the gap |
| Single PR, stacked commits | Chosen over phased PRs to keep the two SDKs from sitting in a partially-ported state across releases |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Shared cross-SDK scenario fixtures (JSON-defined undo scenarios run by both SDKs) | Requires designing a fixture format before any porting starts, and cannot express the SDK-level assertions the JS tests already make |
| Go-only integration tests | Proves convergence within Go, not equivalence with JS — which is the actual goal |
| Go ↔ JS cross-SDK integration tests | Strongest evidence, but needs CI harness running two runtimes. Worth revisiting once the port lands |
| Phased PRs, one per data type | Leaves the SDKs partially diverged across releases |
| Text-first vertical slice | Object / Array / Counter are the cheapest way to validate the history and reconciliation skeleton; deferring them front-loads risk |

## See Also

- [undo-redo.md](undo-redo.md) — the algorithm this port implements
- [tree-split-undo-redo.md](tree-split-undo-redo.md) — boundary-deletion reverse
  operations for `splitLevel >= 1`, ported in Phase 4

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

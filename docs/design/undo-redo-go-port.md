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
| 1 | Object / Array / Counter reverse operations, presence reverse | `history_array_test.go` (84 runtime instances, 4 skipped) |
| 2 | Text `Edit` / `Style` reverse, `reconcileTextEdit` | `history_text_test.go` (64) + `history_text_reconcile_test.go` (9) = 73 runtime instances |
| 3 | Tree `TreeEdit` (`splitLevel` 0) / `TreeStyle` reverse, `reconcileTreeEdit` | `history_tree_test.go` (135 runtime instances), `history_tree_concurrent_test.go` (14 runtime instances, 2 skipped) |
| 4 | Tree split `splitLevel` >= 1 | `history_tree_split_test.go` (79 runtime instances, 1 skipped) — **corrected from this table's original estimate of "26, 1 skipped"**, a pre-port estimate that undercounted the same way the array/tree files did; see Task 21's parity audit |

Phase 0 isolates the widest and riskiest change — the interface signatures —
into a step that provably changes nothing.

### Testing

The JS suite ports file for file. The existing
`test/integration/history_test.go` covers the admin change-history API and is
unrelated, so there is no name collision.

| JS | Go | Runtime instances |
|---|---|---|
| `history_array_test.ts` | `test/integration/history_array_test.go` | 84 (4 skipped) |
| `history_text_test.ts` | `test/integration/history_text_test.go` (64) + pre-existing `history_text_reconcile_test.go` (9, not duplicated — see Task 13) | 73 |
| `history_tree_test.ts` | `test/integration/history_tree_test.go` | 135 |
| `history_tree_concurrent_test.ts` | `test/integration/history_tree_concurrent_test.go` | 14 (2 skipped) |
| `history_tree_split_test.ts` | `test/integration/history_tree_split_test.go` | 79 (1 skipped) — **corrected, see below**; this table originally estimated 26 before any file existed |
| undo cases in `object_test.ts`, `counter_test.ts` | corresponding Go files | — |
| `undo_copy_path_test.ts` (3 `it`s: JS #1 "restores the text without duplicating an id", #2 "does not splice the copy into the chain it came from", #3 "returns to the deleted state on redo") | JS #2 already ported (Task 15): `pkg/document/operations/tree_edit_test.go:66-95`, `TestTreeEditReissueContentIDs`'s "gives copied content a fresh identity test". JS #1 and #3 had no Go equivalent (Task 15's test builds the reverse and asserts on it directly, never executing it through a real `Document.Undo()`/`Redo()`) — ported in Task 21 as `pkg/document/tree_undo_test.go`'s "undo through the copy-reinsert reverse restores content without duplicating an id test" and "...returns to the deleted state on redo test", using a new `Document.PushUndoForTest` test seam in place of JS's `CRDTTree.prototype.edit` monkey-patch, which Go has no equivalent of | — |
| `undo_content_identity_test.ts` | ported: `pkg/document/operations/tree_edit_test.go`'s `TestTreeEditReissueContentIDs` (Task 15) | — |
| `text_restore_after_gc_test.ts` | already covered: `pkg/document/crdt/text_restore_test.go` (`TestTextRestoreExecuteAfterGC`, `TestTextRestoreAfterGCKeepsOrderAcrossInsertions`) — pre-port infrastructure from PRs #1875/#1913 | — |
| `text_restore_convergence_test.ts` (4 `it`s) | its first 3 (convergence regardless of undo order, docSize.gc drained symmetrically) correspond to `pkg/document/crdt/text_restore_test.go`'s `TestTextRestore`'s "overlapping undo converges in both restore orders" — a CRDT-layer test of the same property, not the two-`Document`-with-cross-sync shape JS uses. JS #4 ("reverses GC accounting exactly across delete/undo/redo/undo", asserting `DocSize` bit-identical including the meta term) had no Go equivalent anywhere — ported in Task 21 as `TestTextRestoreDocSizeReversibleAcrossUndoRedoCycle` in the same file | — |
| `restore_converter_test.ts` (5 `it`s) | 3 already covered by `api/converter/converter_restore_test.go`'s `TestRestoreSpanRoundTrip` (restore + retombstone + ordinary-edit cases) — pre-port infrastructure from PR #1875. 2 missing — "round-trips the companion retombstone_spans of a replace reverse" and "decodes to a harmless no-op for peers that ignore restore fields" (the mixed-version wire contract: a restore op's base `Edit` carries empty content and a zero-width range) — ported in Task 21 as `TestRestoreSpanRoundTripsRetombstoneCompanion` and `TestRestoreSpanBaseEditCarriesNoInlineContent` in the same file | — |
| `tree_restore_converter_test.ts` | already covered: `api/converter/converter_tree_restore_test.go` — pre-port infrastructure from PR #1893, **plus a genuine Go-only gap found in Task 21 review: see Critical 3 below** | — |
| `tree_duplicate_id_test.ts` (9 `it`s) | 8 already covered by `pkg/document/crdt/tree_duplicate_id_test.go` — pre-port infrastructure from PR #1927. "does not let a dropped copy widen the reverse operation" was missing (this one is exercised at the operations layer via `TreeEdit.Execute`, not `crdt.Tree.EditT` directly, since the property under test — `GetContentSize()` and the reverse's own range — lives on the operation) — ported in Task 21 as `TestTreeEditDroppedCopyDoesNotWidenReverseOperation` in the same file | — |

**Unit-test audit (Task 21, corrected after review).** A `yorkie-js-sdk`
checkout pinned at this port's exact target commit (`28a5a42e`, v0.7.16) is
available in this workspace at `../yorkie-js-sdk` — an initial pass wrongly
stated otherwise and audited these seven files by matching Go test names
against task-report descriptions instead of reading the JS bodies. Redone
against the actual JS source: of the seven files, one JS `it` maps to a
genuine Go-only defect (Critical 3 — see below, not a porting gap), and six
JS `it`s across four files had no Go equivalent and are now ported (Task 21):
2 in `undo_copy_path_test.ts`, 1 in `text_restore_convergence_test.ts`, 2 in
`restore_converter_test.ts`, 1 in `tree_duplicate_id_test.ts`. The rest were
already covered, either by this port's own Task 15 or by the pre-port
server-side restore infrastructure (#1875, #1893, #1913, #1927 — all merged
to `main` before this port's branch point), under different names than the
JS files — consistent with the port's own framing in the "Problem" section
above, that the server-side half of undo/redo, and its test coverage,
predate this port.

**Critical 3 (Task 21 review): Go accepted a wire payload JS rejects.**
`tree_restore_converter_test.ts:203`'s `it('on an attribute updatedAt')`
has no Go counterpart, and unlike every other gap on this page, **the reason
is that Go did not implement the check** — this is not a porting gap but a
Go-only correctness bug on the server's untrusted-input path. Trace:
`api/converter/from_pb.go`'s `fromTreeRestoreSpans` validates every span's
id/parent/left-sibling/right-sibling `created_at`, but decoded the
attribute snapshot via the shared `fromRHT` without checking `updatedAt` on
each attribute first. `fromRHT` → `fromTimeTicket` returns `(nil, nil)` for
a nil ticket rather than an error, so a crafted payload planted an `RHT`
node with a nil `updatedAt`, which panics on its first `Ticket.Compare`
call deep inside the restore path (`pkg/document/time/ticket.go`'s own doc
comment: "If the receiver or argument is nil, it would panic at runtime").
JS's `fromPbTreeRestoreSpan` validates this up front
(`Object.values(pbSpan.attributes).some((attr) => !attr.updatedAt)`) and
rejects the whole span before any decoder runs. Per the port's own ruling
mechanism (a Go-only gap where JS already validates is not a shared defect
to port-as-is; it is a hardening gap to close), **fixed rather than filed**:
`fromTreeRestoreSpans` (`api/converter/from_pb.go`) now rejects a nil
`UpdatedAt` on any span attribute before decoding, mirroring the existing
id/parent/sibling checks. Pinned by a new subtest, "nil attribute
updatedAt", in `TestTreeRestoreSpanRejectsNilCreatedAt`
(`api/converter/converter_tree_restore_test.go`) — RED (no error returned)
confirmed before the fix, GREEN after. **This is not only the server's
inbound-wire path**: `converter.FromOperations` is the same decoder
`server/backend/database/change_info.go:119`'s `ChangeInfo.ToChange` uses
to materialize a *stored* change, so the new strictness applies
retroactively — a change persisted before this fix with a nil attribute
`updatedAt` would now make that document permanently unloadable. Same
shape as, and folded into, the negative-`splitLevel` stored-data audit
question in the "Rejecting a negative `splitLevel`..." entry of
[20260816-remote-redo-replica-divergence-todo.md](../tasks/active/20260816-remote-redo-replica-divergence-todo.md);
the pre-merge audit there should cover both checks.

**Counting method.** These files build many of their cases through
parameterized loops (`for (const op of ops) { it(...) }`, nested Cartesian
products), so a source-level count of `it(` calls badly understates what
actually runs — e.g. `history_array_test.ts` is 2 `it(` call sites but 84
runtime instances; `history_tree_test.ts` is 32 by the same grep but 135.
The counts above are **runtime instance counts**: derived by reading each
loop's structure and multiplying it out, then confirmed against the actual
`go test -v` output of the ported Go suite, which reports one
`--- PASS`/`--- SKIP` line per instance — **except** for a Go test ported as
a bare `func Test...(t *testing.T)` with no `t.Run` calls inside it (one JS
`it` needing no parameterization, e.g. `TestHistoryTreeUndoPastInitialRoot`):
that emits one unindented, slash-free top-level line, invisible to a naive
"count indented `/`-bearing lines" pass, and has to be added back by hand,
function by function (found in Task 21's review round; see the lessons
file's "Parity audit" section for the full accounting). `history_array_test.ts` and
`history_text_test.ts` were verified this way while porting them (Tasks 8
and 13, `.superpowers/sdd/20260815-undo-redo-go-port-todo/task-8-report.md`
and `task-13-report.md`); `history_tree_test.ts` and
`history_tree_concurrent_test.ts` while porting them (Task 18,
`task-18-report.md`). This is the third task in this port where a
grep-derived `it(` count misled the task brief — treat any count in this
document that was not independently re-derived as suspect until it is.

**Update (Task 21 parity audit).** Of the five skips this table originally
listed, one pair turned out to be stale and is no longer skipped in Go; three
remain skipped, confirmed still real:

- `history_text_test.ts:705`, `:742` — "Case 3/5 correctness". **Stale, not
  carried over.** JS #1293 ("Identity-preserving restore for Text undo/redo",
  2026-07-23) replaced the deep-copy-reinsert undo mechanism these skips were
  written against with the same `restoreSpans`/`retombstoneSpans`
  identity-addressed restore this port also uses — an ancestor of v0.7.16, the
  version this port targets. Nobody re-ran the skipped pair in JS after #1293
  landed (`git log 4b00927c..HEAD -- history_text_test.ts` in `yorkie-js-sdk`
  is empty). Task 13 ported both cases live anyway rather than trusting the
  skip citation, as `test/integration/history_text_reconcile_test.go`'s
  `TestReconcileOverlappingUndoDuplicatesContent`, and they pass — pinning the
  identity-preserving restore against the stale JS skip, not silently
  diverging from it. See this document's lessons file for the general
  principle this established.
- `history_tree_concurrent_test.ts:143`, `:186` — "KNOWN: delete a whole
  `<p>` vs edit text inside it, both undo" and "KNOWN: delete two `<p>` vs
  edit inside first, both undo (segmentation)". Confirmed still skipped as of
  `fa6cc513` — unaffected by that commit, which fixed a different case (see
  `undo-redo.md`'s Tree reconciliation Cases 3-6 note). Still skipped in Go,
  same reason.
- `history_tree_split_test.ts:800-802` — the `split-l2 → split-l2` undo
  chain, `TODO(#1235)`: "the boundary-deletion reverse op doesn't correctly
  restore the state when two consecutive L2 splits produce tombstoned
  structure." Still skipped in Go. Task 20 ran this case with the skip
  temporarily bypassed as an experiment (not shipped) to check whether it was
  also stale like the Text pair above — it is not: the same malformed nesting
  reproduces in Go, confirming the skip is warranted rather than a leftover.

**Do not un-skip any of these unilaterally when porting.** Test count is the
divergence check: a test present in JS and absent in Go is a gap; a test
skipped in JS and un-skipped in Go without independently confirming and
fixing the same defect in JS first is a port that has silently diverged. The
Text pair above is not an exception to this rule — it was un-skipped only
after independently confirming, by reading JS's own git history, that JS's
underlying defect no longer exists.

### What Shipped vs. What This Plan Predicted (Task 21)

The port matches this document's design in substance — the layer boundaries,
the phase sequencing, and the six-case reconciliation all landed as sketched.
Where the *implementation* differs from this document's sketch, it is because
this document described return values conceptually ("prior state needed")
without committing to a concrete Go shape, and the concrete shape that emerged
during implementation is richer than a bare tuple would allow:

- **`Tree.Edit` returns a struct, not a wider tuple.** This document's Layer 2
  table lists `TreeEdit`'s prior-state need as "removed contents,
  `preTombstoned`" without a signature. What shipped
  (`pkg/document/crdt/tree.go`) is:

  ```go
  func (t *Tree) Edit(
      from, to *TreePos, contents []*TreeNode, splitLevel int,
      editedAt *time.Ticket, issueTimeTicket func() *time.Ticket,
      versionVector time.VersionVector,
  ) ([]GCPair, resource.DataSize, TreeEditReverseInfo, error)
  ```

  `TreeEditReverseInfo` (same file) carries `RemovedSpans`/`InsertedSpans`
  (identity-addressed, for the restore-mode reverse), `SpansComplete` (whether
  those spans are trustworthy or the edit must fall back to copy-reinsert),
  `MergeLevel`, `InsertedContentSize`, `Removed`, `PreTombstoned`,
  `PreEditFromIdx` (the reconciliation anchor Lesson 4 above is about — the
  index `from` occupies right after Phase 3, before any mutation), and
  `RemovedSize` — nine fields where a plain 2-tuple would have forced the
  copy-reinsert fallback's quite different needs (raw removed nodes, a
  pre-tombstoned ID set, the pre-mutation anchor and size) to be threaded
  through the same two return slots as the restore-mode path's identity
  spans. A named struct was the only way to keep both reverse
  strategies (identity-preserving and copy-reinsert) expressible from one
  `Edit` call without a second, parallel return path.
- **`Text.Edit` and `Text.Style`/`RemoveStyle` widened by one and two return
  values respectively**, not a struct — these needed fewer new pieces of state
  than Tree did, so a tuple stayed readable:
  `Text.Edit` gained `[]RestoreSpan` (`pkg/document/crdt/text.go`);
  `Text.Style`/`RemoveStyle` gained `[]PrevAttr`.
- **`Operation.Execute` and `Change.Execute` match this document's sketch
  exactly** (`pkg/document/operations/operation.go`,
  `pkg/document/change/change.go`) — these were the two signatures Phase 0
  fixed first, and they didn't move afterward.
- **`History` and `HistoryOperation` match this document's sketch exactly**
  (`pkg/document/history.go`) — the nil-discriminated struct shape held up
  under implementation.

#### Three pre-existing divergences found during the port, not filed before now

None of these are regressions this port introduced — each is either Go code
that predates this port's branch, or a direct, deliberate 1:1 port of JS
behavior that happens to differ from a neighboring piece of JS behavior. They
surfaced only because building the port required reading the relevant JS and
Go source side by side at a level of detail nobody previously had reason to.
Not fixed here, per this document's own rule against one-sided fixes; filed
for whoever picks them up next.

1. **Go's Phase 3 range-narrowing is missing a suppression guard, not a
   `to`-side narrowing step — corrected in Task 21's review, the initial
   audit had this backwards.** JS's `CRDTTree.edit` narrows the collection
   range when a concurrent element split has put `fromLeft` in a different
   parent than `fromParent` (`if (fromLeft !== fromParent && fromParent !==
   toParent)`), then, having found a split sibling in `toParent`,
   deliberately **skips** applying that narrowing in one specific case:
   `if (toLeft !== toParent)` (`crdt/tree.ts:1859-1866`). JS's own comment
   says why — "Skip narrowing when toLeft === toParent (leftmost child
   position, offset 0). The narrowed collectFromLeft would be a child at
   offset >= 1, producing a backwards range that suppresses the intended
   merge." Go's `Tree.Edit` (`pkg/document/crdt/tree.go:1664-1686`, "Phase 3:
   Range Narrowing") performs the same narrowing but has no corresponding
   suppression check — it narrows unconditionally whenever it finds a split
   sibling in `toParent`, including the `toLeft === toParent` case JS
   deliberately excludes. So the divergence is not "Go narrows less than JS"
   but the reverse: **Go narrows in a case JS deliberately does not**, and by
   JS's own reasoning the symptom is a backwards range that suppresses an
   intended merge. Not reached by any test in this port's suite — the
   scenario needs a concurrent element split positioned at the `to` boundary
   of a Tree edit with `toLeft === toParent` specifically, which none of the
   ported multi-client cases construct.
2. **The snapshot-branch replay uses the wrong `OpSource`.** After
   `ApplyChangePack` applies a snapshot, it replays the client's own pending
   local changes via `d.applyChanges(d.doc.localChanges)`
   (`pkg/document/document.go:540`), which executes each change with
   `operations.OpSourceRemote` (`document.go:583`, the same source
   `applyChanges` always uses). JS's equivalent replay
   (`document.ts:1466`) uses `OpSource.Local`. Since `Set` and `Remove`
   genuinely branch on `OpSource` (this document's Layer 1 section, and the
   `OpSourceUndoRedo`-gated deregister step described in
   [20260816-remote-redo-replica-divergence-todo.md](../tasks/active/20260816-remote-redo-replica-divergence-todo.md)),
   this is not merely cosmetic — it is a source mismatch on a path that other
   parts of this port depend on `OpSource` being correct for. This is Go-only:
   there is no JS analogue to file a matching bug against, since JS's own code
   path takes the source it should. Whether this is worth fixing alone (no
   parity cost, since JS has nothing to diverge from) is a decision for
   whoever picks it up — it wasn't in scope for the ported test suite to
   surface, since no test in this port distinguishes `OpSourceLocal` from
   `OpSourceRemote` specifically on the snapshot-replay path.
3. **Go emits no `Snapshot` event after `applySnapshot`.** `ApplyChangePack`'s
   snapshot branch (`pkg/document/document.go:518-523`) calls
   `d.doc.applySnapshot` directly and constructs no `DocEvent`, unlike the
   non-snapshot branch (`applyChanges`, same file), which collects
   `changeEvents` and delivers them over `d.events`. JS emits a
   `DocEventType.Snapshot` event at the equivalent point so subscribers can
   distinguish "document was reset from a snapshot" from "document advanced by
   incremental changes" — a distinction a UI might reasonably want (e.g. to
   avoid replaying a transition animation across a full state reset). This
   predates this port; it surfaced because Task 21's design-doc audit was the
   first pass that read `ApplyChangePack` end to end looking specifically for
   Go/JS event-emission parity, which no prior task's scope required.

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

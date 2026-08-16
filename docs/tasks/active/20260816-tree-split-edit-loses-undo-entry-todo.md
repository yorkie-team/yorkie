# Tree: a splitting edit that also inserts or removes loses its undo entry

**Created**: 2026-08-16

Found by the whole-branch review of the undo/redo Go port
(`docs/design/undo-redo-go-port.md`). Filed rather than fixed — see "Why this
is left as parity" below.

## Problem

`TreeEditOperation.execute` produces a reverse operation in exactly two
cases (`tree_edit_operation.ts:470-487`):

```ts
const isPureSplit =
  this.splitLevel > 0 && !this.contents?.length && removedNodes.length === 0;
if (this.splitLevel === 0) {
  reverseOp = this.toReverseOperation(...);
} else if (isPureSplit) {
  reverseOp = this.toSplitReverseOperation(tree, preEditFromIdx);
}
```

An edit with `splitLevel > 0` that *also* inserted content or removed nodes
matches neither branch, so `reverseOp` stays `undefined`. Go's
`TreeEdit.selectReverseOperation` (`pkg/document/operations/tree_edit.go`)
ports this exactly and returns `(nil, nil)` for the same case.

Both SDKs then push to the undo stack only when a change produced at least one
reverse operation — JS's `if (reverseOps.length)`, Go's `if len(reverse) > 0`
in `Document.Update`. So the edit is applied and **no undo entry is created
for it**. The stack is now misaligned with the user's edit history: the next
`Undo` reverts the edit *before* it.

There is no error and no signal. `CanUndo()` returns true throughout.

### Reproduction

Plain single-client editing on a GC-enabled document, no concurrency:

```text
init:       <r><p>abcd</p></r>          undoStack=1
after A:    <r><p>abcdZ</p></r>         undoStack=2   (insert "Z" at 5)
after B:    <r><p>abX</p><p>cdZ</p></r> undoStack=2   (Edit(3,3,"X",splitLevel 1)
                                                       -- split + insert,
                                                       pushed nothing)
after Undo: <r><p>abX</p><p>cd</p></r>  undoStack=1
```

One `Undo` press leaves B applied and silently deletes A's `"Z"`. The user
asked to undo the split; they got the previous edit reverted instead, and the
split kept.

### Why the reverse is withheld in the first place

The withholding is not an oversight. A splitting edit that also inserted or
removed needs a reverse that undoes *both* at once — merge the boundary back
**and** revert the insertion/removal — and neither builder produces that.
`toReverseOperation` reverses content changes but not the split;
`toSplitReverseOperation` reverses the split but not the content. Emitting
either alone would corrupt the tree structurally, which is worse than emitting
nothing. The defect is not "no reverse is built" but "nothing records that a
reverse was owed".

## Why this is left as parity, and why the no-op sentinel was rejected

Go's port is faithful: the gate, the builders, and the push condition all
match JS. Per this port's governing rule, a defect JS still has is reproduced,
not fixed.

Unlike the Tree Style combined-reverse defect (filed separately), the
"Go is also the server" argument does **not** apply here: the undo/redo stacks
are purely client-side bookkeeping. Nothing about them reaches the wire, a
snapshot, or a change log. So a Go-side fix would not create server-versus-
client divergence, and the review explicitly left the call open.

A no-op sentinel entry — push an empty history entry so the stack depth stays
aligned with the edit count — was considered and **rejected**, because it does
not fix the defect:

- With the sentinel, the first `Undo` becomes a visible no-op and the *second*
  `Undo` produces exactly the state the first one produced without it:
  `<r><p>abX</p><p>cd</p></r>`. B is still applied, A's `"Z"` is still gone.
  The scrambled outcome is deferred by one press, not prevented.
- Meanwhile it diverges observably from JS: `CanUndo()`, the number of presses
  needed, and the stack depth all differ. And the redo side goes the other way
  — an entry that executes nothing pushes nothing onto the redo stack, so the
  stacks misalign in the opposite direction.

Trading a shared, uniform defect for a cosmetic divergence that leaves the
defect in place is strictly worse. The honest fix is to build the combined
reverse, in both SDKs.

## Tasks

- [ ] Decide the real fix: a reverse operation that both merges the boundary
      back and reverts the content change — likely a `TreeEdit` carrying a
      `redoSplitLevel` alongside its restore/retombstone spans, so one reverse
      undoes both halves
- [ ] Fix in `yorkie` and `yorkie-js-sdk` together. Not urgent to keep them in
      lockstep here (the stacks never reach the wire), but the port exists to
      stop the two from drifting, and a one-sided fix restarts the drift
- [ ] Until then, consider whether `Document.Undo`'s API documentation should
      state that a splitting edit combined with an insert or remove is not
      undoable, so callers of the new Go public API are not surprised
- [ ] Add a Go test pinning the current behavior once the shape of the fix is
      decided, so the change from "silently loses the entry" to "undoes both
      halves" is a deliberate, visible test edit

## See Also

- `docs/design/undo-redo-go-port.md` — the port, and the Risks row for this
- `docs/design/tree-split-undo-redo.md` — `toSplitReverseOperation`, the
  builder that covers the pure-split half of this gate
- `docs/tasks/active/20260816-tree-style-combined-reverse-dropped-todo.md` —
  the other Tree defect ported as-is, where the server-replay argument *does*
  apply

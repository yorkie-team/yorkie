# CRDT: removing a non-empty container corrupts DocSize's GC total

**Created**: 2026-08-16

Found during the final whole-branch re-review of the undo/redo Go port
(`docs/design/undo-redo-go-port.md`). Initially misattributed as a
consequence of that port's Critical 1 fix (a negative `DocSize.GC` figure
showed up in a regression test's RED output); it is not. **This bug is
pre-existing on `main`, in code the port never touches, and is neither
caused nor fixed by the undo/redo port.** See "Not caused by the port"
below.

## Problem

`Root.RegisterRemovedElementPair` (`pkg/document/crdt/root.go:157-168`) is
the only place a removed element's cost moves from `Live` into `GC`:

```go
func (r *Root) RegisterRemovedElementPair(parent Container, elem Element) {
	r.docSize.GC.Add(elem.DataSize())
	r.docSize.Live.Sub(elem.DataSize())
	...
}
```

It adds/subtracts `elem.DataSize()` for the removed element **only** — not
its descendants. It is called exactly once per removal, with the removed
container itself as `elem` (e.g. `pkg/document/json/object.go:313`, the
`Object.Delete` path). For a container type, `DataSize()` returns only the
container's own metadata, excluding descendants — see
`Object.DataSize()` (`pkg/document/crdt/object.go:128`):

```go
func (o *Object) DataSize() resource.DataSize {
	return resource.DataSize{
		Data: 0,
		Meta: o.MetaSize(),
	}
}
```

So deleting a non-empty object moves only the object's own meta size into
`GC`; every descendant's size (added to `Live` when each descendant was
created, via `Root.RegisterElement`, `root.go:112-120`) is left sitting in
`Live` untouched.

Later, when the tombstoned container is actually collected,
`Root.deregisterElement` (`root.go:132-154`) does the opposite walk — the
container **and every descendant**:

```go
func (r *Root) deregisterElement(element Element) int {
	...
	deregister := func(elem Element) {
		...
		r.docSize.GC.Sub(elem.DataSize())
		...
	}
	deregister(element)
	if element, ok := element.(Container); ok {
		element.Descendants(func(elem Element, parent Container) bool {
			deregister(elem)
			return false
		})
	}
	...
}
```

Each descendant's `DataSize()` is subtracted from `GC` — but no descendant
was ever *added* to `GC` (only the container itself was, in
`RegisterRemovedElementPair`). The asymmetry:

- **Add side** (`RegisterRemovedElementPair`, root.go:157-168): container
  only.
- **Subtract side** (`deregisterElement`, root.go:132-154): container **and**
  every descendant.

The result: `GC` is over-subtracted by every descendant's `DataSize()`, and
`Live` is never decremented for those same descendants, so their cost is
permanently stranded in `Live` — a document that has genuinely shrunk to
empty still reports a nonzero `Live` size, offset by a negative `GC` size
that happens to cancel it out in `Total()` only by coincidence of this
specific case.

### Reproduction

Verified directly against this branch's checkout before making any other
change in this filing (scratch test, since removed). Four lines, no undo, no
second client, no skip path involved:

```go
doc.Update(func(root *json.Object, _ *presence.Presence) error {
	root.SetNewObject("k").SetString("a", "1")
	return nil
})
doc.Update(func(root *json.Object, _ *presence.Presence) error {
	root.Delete("k")
	return nil
})
doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
```

Output:

```
before GC: {Live:{Data:2 Meta:72} GC:{Data:0 Meta:72}}
collected: 2
after GC:  {Live:{Data:2 Meta:72} GC:{Data:-2 Meta:-48}}
```

`GC.Data` and `GC.Meta` both go negative after collecting the very elements
that produced them, and `Live` never drops back to zero even though the
document is now empty. The same code exists verbatim on `main`
(`bfe2de7d` at time of writing) — confirmed by diffing `root.go` against
`main`; the port never touched `RegisterRemovedElementPair`,
`deregisterElement`, or any `DataSize()` implementation.

## Why it matters

`Document.DocSize()` (`pkg/document/document.go:774-776`) exposes exactly
this accounting, and `Document.Update` (`pkg/document/document.go:252-253`)
gates `MaxSizeLimit` on it:

```go
cloneSize := d.cloneRoot.DocSize()
if !ctx.IsPresenceOnlyChange() && d.MaxSizeLimit > 0 && d.MaxSizeLimit < cloneSize.Total() {
	return ... // reject the update
}
```

`DocSize.Total()` is `Live.Total() + GC.Total()`. Once `GC.Total()` goes
negative, it silently discounts the reported total below the document's
real cost. A server or client enforcing `MaxSizeLimit` can therefore accept
updates it should reject, and the drift compounds with every removal of a
non-empty container — each one strands more `Live` size and pushes `GC`
further negative. There is no error, no panic, and no test today that
asserts `GC` stays non-negative.

## Not caused by the port

- `RegisterRemovedElementPair`, `deregisterElement`, and every `DataSize()`
  method involved are byte-identical between this branch and `main`
  (`bfe2de7d`).
- The undo/redo port added no new caller of `RegisterRemovedElementPair`
  that passes a container with live descendants in a way `main` did not
  already allow — `Object.Delete` (`json/object.go:313`) is a pre-existing
  forward-edit path, not new undo/redo code.
- The port's own final-fix-report (`.superpowers/sdd/20260815-undo-redo-go-port-todo/final-fix-report.md`,
  Critical 1) originally quoted this bug's negative-`GC` signature as part of
  that fix's RED output. That was a correct observation of a real failing
  assertion, but an incorrect attribution of *why* the number was negative —
  the divergence Critical 1 fixed (a skipped operation reaching the executed
  list) is real and unrelated; the negative sign in the expected baseline
  value came from this bug. The report has been corrected alongside this
  filing.

## Second manifestation: `DeregisterElement(value)` books the wrong element

Raised by CodeRabbit on PR #1932 against `pkg/document/crdt/root.go:129`
and its only caller, `Set.Execute` (`pkg/document/operations/set.go`).
**Same root cause as above — `deregisterElement` subtracting a size that
was never added — reached by a different route, so it is folded in here
rather than filed separately.** Both the call site and
`deregisterElement` are faithful ports: JS's `set_operation.ts:98-104`
calls `root.deregisterElement(value)` with the same incoming value, and
JS's `deregisterElement` subtracts `element.getDataSize()` the same way
(`root.ts:232-248`).

During undo/redo a `Set` may restore an element under a `createdAt` that
is already registered — undoing a `Remove` re-inserts the removed element
under its original identity. `Set.Execute` clears the stale entry first:

```go
if source == OpSourceUndoRedo && root.FindByCreatedAt(value.CreatedAt()) != nil {
	root.DeregisterElement(value)
}
root.RegisterElement(value)
```

`value` here is the freshly deep-copied element the operation is about to
install, **not** the tombstoned element currently registered under that
`createdAt`. `deregisterElement` then does two things with the wrong
object:

- `r.docSize.GC.Sub(elem.DataSize())` charges the *incoming* element's
  size against `GC`, where the size that entered `GC` was the
  *tombstoned* element's. They are equal only when the two happen to
  hold identical content.
- The descendant walk (`element.Descendants(...)`) enumerates the
  *incoming* element's children. A descendant the tombstone had but the
  copy does not stays registered in `elementMap` and `gcElementPairMap`
  forever; a descendant the copy has but the tombstone did not is
  subtracted from `GC` having never been added to it.

The second bullet is the same add/subtract asymmetry as the top of this
document, which is why the fix has to be decided once, for both. Looking
up the registered element (`root.FindByCreatedAt(value.CreatedAt())`) and
deregistering *that* is the obvious repair for this call site, but it
only makes sense alongside whichever symmetry the tasks below settle on.

## Tasks

- [ ] Decide the fix shape: either make `RegisterRemovedElementPair` walk
      descendants the same way `deregisterElement` does (add each
      descendant's `DataSize()` to `GC` / subtract from `Live` at removal
      time, not just the container), or make `deregisterElement` subtract
      only the container's own size and rely on some other bookkeeping for
      descendants. The two functions must become symmetric one way or the
      other.
- [ ] Add a regression test asserting `DocSize().GC` and `DocSize().Live`
      never go negative, and that `DocSize().Total()` returns to the
      pre-mutation baseline after a full add-then-remove-then-GC cycle of a
      non-empty container (object, array, or tree with children).
- [ ] Fix `Set.Execute`'s `DeregisterElement(value)` to deregister the
      element actually registered under that `createdAt`, not the
      incoming copy — and raise the same correction with the JS SDK,
      since `set_operation.ts:98-104` has it too.
- [ ] Audit whether the same asymmetry exists for `Array`, `Tree`, and other
      `Container` implementations, not just `Object` — the reproduction
      above only exercises `Object`.
- [ ] Once fixed, re-check whether `TestHistorySkippedUndo`
      (`test/integration/history_test.go`, added by the undo/redo port) can
      have its expected `DocSize` values simplified now that the negative-GC
      artifact is gone.

## See Also

- `docs/design/undo-redo-go-port.md` — the port whose review surfaced this
- `.superpowers/sdd/20260815-undo-redo-go-port-todo/final-fix-report.md` —
  Critical 1, where the misattribution was made and then corrected

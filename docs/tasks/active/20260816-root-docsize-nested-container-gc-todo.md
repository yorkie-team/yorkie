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

## Third manifestation: a concurrent remove books the same element twice

Found while fixing the above, and more severe than either: it is **replica
divergence**, not just a local miscount. `ElementRHTNode.Remove`
(`pkg/document/crdt/element_rht.go:41-48`) returns true again whenever a
later ticket wins the LWW comparison against the `removedAt` already set,
so `Remove.Execute` calls `RegisterRemovedElementPair` a second time for an
element whose size is already in `GC`. Nothing subtracts the surplus.

Two replicas that remove the same container concurrently converge on
content and **disagree on size**, because whether the second registration
happens depends on which ticket each replica saw first. Measured on `main`
before the fix, after both removes and a full GC:

```text
d1: {Live:{Data:2 Meta:24} GC:{Data:-2 Meta:24}}   ({} on both)
d2: {Live:{Data:2 Meta:72} GC:{Data:-2 Meta:-48}}
```

`DocSize` gates `MaxSizeLimit`, so one replica can accept an update the
other rejects, permanently. Pinned by `TestDocumentSize/concurrently
removing the same container test`.

Note the SDKs differ here: Go's `DeleteByCreatedAt` returns nil when the
remove loses LWW and `Remove.Execute` skips the registration, while JS's
`ElementRHT.delete` (`element_rht.ts:119-130`) ignores `node.remove`'s
return value and `remove_operation.ts:100` registers unconditionally. That
is why the pre-fix Go replicas diverged from each other while the JS ones
were uniformly wrong. Making the registration idempotent removes the
dependence on that difference entirely.

## Fourth manifestation: an element born inside a tombstone is never in GC

A remote `Set` can land inside a container that is already removed on this
replica — d1 removes `k` while d2 concurrently writes `k.b`. `Set.Execute`
calls `RegisterElement(b)`, which books `b` into **Live**, and nothing ever
moves it: `b` has no `removedAt` of its own and its container's removal has
already happened. When the tombstone is finally collected,
`deregisterElement` subtracts `b` from **GC**, which never held it. `GC`
goes negative by `b`'s size and `Live` keeps it forever.

This one is not fixed by walking descendants at removal time — the element
does not exist yet when the walk runs — which is what made the first two
attempts at this fix insufficient.

## The fix: name the invariant, then keep it

The three add/subtract sites were each individually plausible and jointly
inconsistent, so the fix is to state what they are collectively maintaining
and make each site honour it:

> Every registered element's `DataSize` is counted in exactly one of
> `Live` or `GC`, and `Root.sizeInGC` records which — and, for GC, the
> exact amount charged.

- `moveSizeToGC(elem)` is the only way a size enters `GC`. It is idempotent:
  a size already there is not moved again, which covers both the repeated
  concurrent remove and the descendant whose container is removed after it
  was individually removed.
- `RegisterRemovedElementPair` calls it for the element and, when the
  element is a `Container`, for every descendant — the descendants are in
  `Live` because `RegisterElement` put them there.
- `deregisterElement` subtracts from `GC` only what `sizeInGC` says was
  charged, and subtracts from `Live` anything not listed there (the fourth
  manifestation).

`sizeInGC` stores the charged `DataSize` rather than a flag because
`DataSize()` is **not stable over an element's lifetime**: it grows by one
ticket the moment `removedAt` is set, and that can happen *after* the size
moved (a member removed remotely inside an already-removed container).
Recording the amount makes the two sides symmetric without depending on
when each side happens to read `DataSize()`; `moveSizeToGC` tops up the
difference when it sees an already-charged element whose size has grown.

The alternative from the original filing — shrinking `deregisterElement` to
the container's own size — was rejected: `Live` is populated by the
descendant-walking `RegisterElement`, so descendants have to leave `Live`
somewhere, and dropping them from the subtract side would strand them there
permanently instead.

## Tasks

- [x] Decide the fix shape — see "The fix" above.
- [x] Add regression tests, all five RED on `main` and GREEN with the fix,
      each asserting the document's size returns to the empty-document
      baseline after remove-then-GC (and, where two replicas are involved,
      that they agree):
      `TestDocumentSize/removing a non-empty container test` (object, array,
      nested object), `.../removing a container holding an earlier tombstone
      test`, `.../concurrently removing the same container test`,
      `.../removing a member inside an already removed container test`,
      `.../restoring a container over a diverged tombstone test`.
- [x] Fix `Set.Execute`'s `DeregisterElement(value)` to deregister the
      element actually registered under that `createdAt`, not the
      incoming copy. The same correction is owed to JS's
      `set_operation.ts:98-104`. Covered by the "restoring a container over
      a diverged tombstone" test, which is the case where the tombstone and
      the incoming copy actually differ.
- [x] Audit the other `Container` implementations: `Object` and `Array` are
      the only two (`Text` and `Tree` are leaf `Element`s whose `DataSize()`
      already covers their whole structure, so they were never affected —
      verified empirically alongside the object/array cases).
- [x] Re-check `TestHistorySkippedUndo` (`test/integration/history_test.go`):
      nothing to simplify. It asserts `d1.DocSize() == d2.DocSize()` rather
      than hardcoding the negative-GC figures, so it needed no edit and
      still passes.

## Confirmed in the JS SDK

`registerRemovedElement` (`packages/sdk/src/document/crdt/root.ts:257`) has
the identical shape — no descendant walk, no idempotence — and
`deregisterElement` (`root.ts:232-253`) walks descendants exactly like Go's.
Running the same four scenarios through the JS SDK produced figures
identical to Go's pre-fix output:

| scenario | JS `gc` after collection | Go on `main` |
|---|---|---|
| object with one member | `{data:-2, meta:-48}` | `{Data:-2 Meta:-48}` |
| array with one element | `{data:-2, meta:-24}` | `{Data:-2 Meta:-24}` |
| nested object | `{data:-2, meta:-96}` | `{Data:-2 Meta:-96}` |
| text | `{data:0, meta:0}` | `{Data:0 Meta:0}` |

`set_operation.ts:98-104` has the wrong-element `deregisterElement(value)`
too. The one place the SDKs differ: Go's `DeleteByCreatedAt` returns nil
when a remove loses the LWW comparison and `Remove.Execute` skips the
registration, while JS's `ElementRHT.delete` (`element_rht.ts:119-130`)
ignores `node.remove`'s return value and `remove_operation.ts:100`
registers unconditionally. That difference is why the pre-fix Go replicas
diverged from *each other* on the concurrent remove while the JS ones were
uniformly wrong; making the registration idempotent removes the dependence
on it.

The JS fix is not in this change and needs its own PR.

## Follow-up found while fixing: snapshot rebuild adds one ticket per removal

Not fixed here, and **not** introduced by this change — the drift is
identical before and after it. `NewRoot` (`root.go:53-93`) calls
`RegisterElement(root)` on a tree whose tombstones already carry
`removedAt`, so that ticket is counted into `Live`; then each removed
element goes through `RegisterRemovedElementPair`, whose
`Live.Meta += time.TicketSize` compensation exists for the incremental path
where the ticket was *not* yet counted at registration time. The rebuild
therefore over-reports `Live.Meta` by exactly one `TicketSize` per removed
element:

```text
one element removed:  live {Data:0 Meta:72}   rebuilt {Data:0 Meta:96}
two elements removed: live {Data:0 Meta:24}   rebuilt {Data:0 Meta:72}
```

A server rebuilding a document from a snapshot therefore reports a larger
`DocSize` than a client holding the same document incrementally, which is
the same `MaxSizeLimit` divergence in a different disguise. Fixing it means
changing the compensation's contract, which changes the reported size of
every snapshot-rebuilt document, so it wants its own task.

The figures above are from `main`. This change reduces the drift to a
constant `TicketSize` instead of one per removed element (measured on the
same document: `+24` after one removal and after two, where `main` gave
`+24` and `+48`), because the idempotent `moveSizeToGC` stops the rebuild's
per-tombstone pass from compensating more than once. It does not remove it.

## See Also

- `docs/design/undo-redo-go-port.md` — the port whose review surfaced this
- `.superpowers/sdd/20260815-undo-redo-go-port-todo/final-fix-report.md` —
  Critical 1, where the misattribution was made and then corrected

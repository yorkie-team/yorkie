**Created**: 2026-09-27

# Mark LWW losers removed and stop leaking GC size records — lessons

## A document-level test found what the unit repro could not

The crdt-level repro passed as soon as the loser was marked removed. The
two-replica test still differed by exactly one ticket of `GC.Meta`: on the
replica that received the loser first, it won on arrival and was stamped with
`movedAt = createdAt`; on the other it lost on arrival and never was. The
removedAt tickets agree, so this is a difference in the elements themselves,
not in how they are accounted, and the JS SDK shares it. It is out of scope
here and filed as a known limitation. Converging on "equal docSize" needs a
test that drives both delivery orders, not one that inspects one replica.

## Gate a state change on the value being changed

The losing branch asked whether the *occupant* was removed, to decide whether
to remove the *incoming* value. The two are unrelated: the occupant's state
decides nothing about whether the loser is still live. When a guard exists to
keep an operation from misfiring, gate it on the object the operation touches.

## Retire a record where the thing it guards stops being reachable

JS closed the `sizeInGC` leak by switching to a `WeakMap`, which ties the
record's lifetime to the element's. Go has no weak-keyed map for an interface
key, and a lifetime tied to the runtime GC is not something a test can pin.
The Go equivalent is to name the moment the element stops being addressable
-- a restored copy taking its `elementMap` slot -- and retire the record
there. The guard the record provides (a peer removing inside an orphan it can
still reach) is kept by a second test, so the retirement cannot creep past it.

## Self review

Round 1 (correctness/tests), a separate reviewer agent working on a copy of
the tree. No blocking findings, so the loop stopped after one round, as the
workflow says to. The reviewer reverted each fix on its own, and every new
test failed without it. The new tests also passed 30 repeated runs. It found
no route back to a released element once its slot is taken over: every
operation resolves through `elementMap`, `SetWithExecutedAt` re-points the
parent's `nodeMapByCreatedAt` at the copy, and `release` drops the orphan's
element pairs. Non-blocking findings, carried into the PR as known
limitations:
- The convergence test pins the existing `movedAt` difference (one ticket
  of `GC.Meta`). Fixing that difference will require updating the assertion.
- The internal test repeats `Set.Execute`'s restore order by hand, so a
  reordering in `set.go` would not be caught there. The real `Undo` path was
  checked once by hand (0 records with the fix, 102 without).
- An orphaned Text/Tree's internal tombstones stay in `gcNodePairMap` until
  they are collected, so the orphan is freed only after that.
- Existing and out of scope: edits inside a still-addressable orphan member
  charge Live, and nothing ever collects them.

## A removal that can decline is not a removal you can assume happened

The review panel's security lens caught the sequel to "gate a state change on
the value being changed": the loser branch now removes the right object, but
it never asked whether `Element.Remove` accepted the ticket. Every
implementation refuses one that does not follow the element's own `createdAt`,
and `createdAt` is decoded from client bytes independently of the operation's
`executedAt` -- so a crafted value can lose the comparison and still refuse the
tombstone. A causal log cannot produce that, which is exactly why the
assumption reads as safe and is not.

The fix is to make indexing conditional on the removal, not to find a ticket
that always works: there is none, and fabricating one (`MaxLamport`) would
trade a live leak for a tombstone no version vector ever covers -- still
emitted into every snapshot. Refusing the value leaves the document as it was.
That also fixes the duplicate-apply tie, where the second copy of an already
applied `Set` used to displace the live copy in `nodeMapByCreatedAt`.

## A guard that only the guarded layer can see is not a guard

All four review lenses landed on the same hole from different angles: the
refusal was implemented inside `ElementRHT.SetWithExecutedAt`, but its return
value said `nil` for both "nothing was evicted" and "I refused your value".
`Set.Execute` could not tell the two apart, so it went on to call
`root.UnregisterRemovedElementPair(value.CreatedAt())` and
`root.RegisterElement(value, obj)` on a value that lives in neither member
map. That reintroduces, one layer up, exactly what the refusal was for: the
value is charged to `docSize.Live`, takes over its `createdAt`'s `elementMap`
slot from whatever live copy is really there, and -- in the
`Set`/`Remove`/replayed-`Set` sequence -- retires the collection entry of a
tombstone that is still indexed, releasing its `GC` charge and making it
uncollectable forever.

The signature is the fix. `SetWithExecutedAt` now returns
`(removed Element, indexed bool)` all the way up through `Object`, and
`Set.Execute` returns an empty `ExecutionResult` -- no reverse, not
observable -- when `indexed` is false. Nothing changed, so there is nothing to
undo and nothing to notify.

The general shape: when a low-level call gains a new "I declined" outcome,
the first question is whether any caller's next line assumes the old one. An
overloaded sentinel guarantees the answer is no.

## Round 4 (panel): "every caller" means every caller, not every operation

The first pass widened `SetWithExecutedAt` and guarded `Set.Execute`, but
left `ElementRHT.Set`/`Object.Set` collapsing the pair back to one value on
the grounds that their one production caller -- `json.Object.setInternal` --
issues a fresh ticket and therefore always wins. All three lenses rejected
that: the argument is about the *operation*, while the dropped return is a
property of the *signature*, so it has to be re-derived by every future
caller and by any reader auditing the guard. Both now return
`(Element, bool)`, and `setInternal` returns early without Root bookkeeping
and without pushing the `Set` -- nothing changed locally, and a peer
replaying it would refuse it too.

The second call site was `fromJSONObject`, which replays members into a fresh
`ElementRHT` as a bare statement. That is a reconstruction, not an operation:
a refused member has nowhere else to go, so it would simply be absent from
the decoded object with nothing recording it. Since the same decoder reads
the element payload of a client-pushed `Set`/`Add`/`ArraySet`, silence there
is a member an attacker can delete from someone else's object. It now fails
the decode with `ErrRefusedMember`.

## A refusal keyed on the value's own state cannot cover a forged one

The security lens found the hole the refusal left open: it fires only when
the loser must be tombstoned and cannot be. A loser that arrives *already*
removed skips the tombstoning, so it was indexed unconditionally -- and a
change carrying both a `removedAt` and the `createdAt` of a live member would
re-point `nodeMapByCreatedAt`, and `Root.elementMap` behind it, at a
tombstone that answers to no key. `createdAt` and `removedAt` are decoded
independently from client bytes, so nothing upstream forbids that pair.

The fix belongs where the invariant is, not where the forgery arrives: the
loser branch now refuses any value whose `createdAt` is already held by a
different node. A causal change log issues a fresh `createdAt` per value and
never reaches it. The lesson is that a guard derived from the value's own
fields (`RemovedAt() == nil`) protects only values that fill those fields
honestly; the guard that holds regardless is the one stated over the
structure being mutated -- here, "this slot is taken".

## A per-container guard cannot state a document-wide invariant

The panel's next round pointed at the same shape one level up. `ElementRHT`
now refuses a loser whose `createdAt` a live node of *that object* already
holds, but `Root.elementMap` is keyed by `createdAt` for the whole document:
a value carrying the `createdAt` of a live element in any *other* container
passes the hashtable's guard untouched, and `RegisterElement` then re-points
the slot at it. The guard has to be stated where the map is. `Set.Execute`
now refuses, before it mutates anything, a value that arrives already removed
and collides with an element `Root` already knows -- the one shape that skips
the tombstoning and can be indexed without winning a key.

The mirror of it was on the winning branch: `UnregisterRemovedElementPair`
documented "the restore re-pointed the index" as a precondition, but the call
site only established that the restore *took*, never that the collection
entry under that `createdAt` named a tombstone of *this* object. An entry
another container registered is untouched by this Set, so retiring it
released a charge the document still carries and left a tombstone nothing
could reach. It now takes the owning container and does nothing when the
entry is someone else's.

## Validate the ticket triple where it enters, not where it hurts

Every one of these refusals exists because `createdAt`, `movedAt` and
`removedAt` are decoded independently from client bytes and nothing checks
that they agree: `Change.SetActor` rewrites only the operation's own
`executedAt`, and `sanitizeElement` only dropped split links. Two of the
impossible triples are cheap to reject at the converter boundary, where the
document has not been touched and the error still names the change that
carried it: a `movedAt` older than the element's own creation, and a
`removedAt` that does not follow it (exactly the precondition `Element.Remove`
enforces, so such an element could never be tombstoned or collected). The
downstream refusals stay -- they are the invariant, and stored changes
predating this check still replay through them -- but they should not be the
first line.

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

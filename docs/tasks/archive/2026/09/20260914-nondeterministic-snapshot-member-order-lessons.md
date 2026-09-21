# Lessons — nondeterministic snapshot member order

**Created**: 2026-09-14

## A correct decoder hides an incorrect encoder

`SetWithExecutedAt` is order-independent, and every Go test round-trips
through it, so nothing in this repo could see that the encoder emitted
members in a different order each time. The randomness only became a defect
at the boundary with a decoder that is order-sensitive — which is in another
repository. Order independence on the read side is a property worth having,
but it is not a substitute for the write side being a function of its input.

## "Unordered" is a property of the container, not of the wire

`nodeMapByCreatedAt` is a map, so its iteration order carries no meaning.
The `repeated RHTNode` field it is copied into is a list, and the receiver
replays it in order. The conversion from one to the other is where meaning
gets invented, and it was invented differently on every call.

Protobuf map fields are the inverse case: they really are unordered on both
sides, so their randomized serialization is harmless — but it still defeats
byte comparison, which is how the invariant gets tested. `Deterministic:
true` is the cheap way to keep the assertion available.

## Symmetry is not a reason

`ElementRHT.Nodes` and `RHT.Nodes` both range a Go map, so sorting both
looked like the same fix applied twice. It was not: an object's members are a
**repeated** protobuf field whose order the receiver replays, while a text or
tree node's attributes are copied into a Go map and emitted as a protobuf
**map** field, which erases the slice order before it reaches the wire. The
second sort changed no output at all and cost 7% on the text-editing path —
and the comment justifying it asserted a mechanism that a five-minute check
disproves (remove the sort, keep `Deterministic`, tests stay green).

The tell was available the whole time: the fix landed, the tests went green,
and nobody asked which of the two changes had made them green.

## Enumerate the permutations rather than trusting one

`TestSnapshotDecodeIsOrderIndependent` runs every permutation of the wire
members instead of one shuffle. With the member count small that is free, and
it is the difference between "this order happens to work" and "order does not
matter". Asserting the re-encoded bytes as well as the visible members is
what makes it meaningful for tombstone timestamps, which `Marshal` never
shows.

## Claiming a test is green at base means running it at base

The writeup said `TestSnapshotDecodeIsOrderIndependent` "passed before the
fix", and that claim was load-bearing — it is what classifies the test as a
baseline rather than a regression test, and what shows Go's decoder was never
implicated. It was true when written and false by the time it shipped: a byte
round-trip assertion added later inherits the nondeterminism under test, so
the test as committed fails at base. The object-equality half still passes,
which is the part the claim was really about.

A claim about a revision is a command that can be run. Run it.

## The reproduction was in production before it was in a test

The symptom — a shape that rendered on some page loads and not others —
looked like a rendering bug for a long time. What identified it was attaching
a bare SDK client repeatedly and printing the same two fields each time: 12
attaches, 3 clean. Nothing about the shape of the bug was visible until the
same read was repeated; a single read looks fine 25% of the time and looks
like corruption the rest.

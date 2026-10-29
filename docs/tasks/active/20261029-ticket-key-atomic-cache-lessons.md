# Lessons: Make the lazy ticket and node-ID key caches race-free

**Created**: 2026-10-29

## "Only these are shared" needs a trace, not a reading of ownership

#2301 kept the caches lazy and warmed three singletons, because every other
ticket is said to live behind one document's lock. That is true for
`*Document`. It is false for the server's snapshot cache:
`DeepCopy` reuses ticket and node-ID pointers, so the copy each request gets
shares them with the cache and with every other request. A claim that only
some instances are shared has to be checked against every place that copies a
pointer: caches, `DeepCopy`, read-locked walks.

## Write the race test against the real sharing pattern

The read-only versions of the reported paths do not race on main. Concurrent
`DeepCopy` and `GarbageLen` under `RLock` both only read keys that
`RegisterElement` already warmed. The race only shows up when a clone
*mutates*: a deletion keys a node ID that was never keyed. A test that only
reads would have passed on main and proved nothing. The useful test replays
what `BuildInternalDocForServerSeq` does: deep-copy the cached document, then
apply changes to the copy.

## Measure allocs, not wall-clock

Shared CI runners and laptops swing ns/op by tens of percent on unrelated
benchmarks. allocs/op and B/op come out the same on every run, and they are
what separated the eager design (+21–149% allocs) from this one (+0.7%
geomean).

## A lazy cache is part of the struct's equality contract

The first cut cached in `atomic.Pointer[string]`. That holds a `*string`, and
`reflect.DeepEqual` compares a pointer field by address: two tickets with the
same lamport, delimiter and actor stopped being deep-equal as soon as both had
built their key. `assert.Equal` falls through to `reflect.DeepEqual` for
`*Ticket`, and tests across the repo compare tickets that were built
independently — the `removedAt` of the same node in two client documents, a
ticket before and after a protobuf round trip.

`atomic.Value` is race-free the same way but boxes the key in an interface,
and `DeepEqual` compares an interface by its dynamic value. Two warmed caches
holding the same key stay equal, and a warmed cache still differs from a cold
one exactly as the plain `string` field did on main. Switching to it restores
main's equality semantics with no change to the `Key()` contract.

The lesson generalises: swapping a value field for a pointer-shaped one is not
a private implementation detail if the struct is ever compared structurally.
`pkg/document/time/ticket_test.go` and `pkg/document/crdt/rga_tree_split_test.go`
now pin this — both subtests fail against the `atomic.Pointer` version.

The cost is the `noCopy` that `atomic.Pointer` embeds and `atomic.Value` does
not, so vet copylocks no longer proves on its own that nothing copies these
structs by value. The structs were already shared by pointer everywhere.

## Self review

One round covering correctness/tests and design fit. It found nothing
blocking. What it checked:

- No other lazy caches sit on the shared-pointer path. `TreeNodeID` and the
  `RHTNode` keys build their strings fresh through `Key()`.
- The apply-on-clone subtests fail on main; with the change, `-count=30`
  passes 210/210.
- No struct value copies.

Applied from the non-blocking findings:

- Dropped the unused `cachedActorIDBase64` cache. `Ticket` shrinks to
  32 bytes.
- Marked the read-only subtests as regression guards.
- Reworded the doc comments so they no longer describe the removed `init()`.

Left as known limitations:

- The apply-on-clone subtest does not exercise `Style` or Array move. Both
  key their tickets through the same atomic `Key()`.
- Integration tests that `assert.Equal` on `*Ticket` were not run, because
  they need MongoDB. The deep-equality regression guards above cover the
  mechanism at pkg level; the integration lane runs on push.

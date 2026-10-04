**Created**: 2026-09-29

# Harden Set against payloads no replica can produce — lessons

Rounds 1-6 below were written on #2069, before this work moved to its own
branch. They are kept verbatim.

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

## Review round 4 (panel): what the new refusals broke

Three blocking findings, all of them the same shape -- a refusal stated at
the right place but reaching one caller too many.

**A boundary rule has to admit what undo really emits.** The
`movedAt must not precede createdAt` half of `validateTicketTriple` read as
impossible and is not: `Document.executeUndoRedo` re-identifies the value of
an `Add`/`ArraySet` reverse with a freshly issued `createdAt`
(`document.go:465-472`) and leaves the copy's older `movedAt` -- stamped by
`RGATreeList.MoveAfter` -- alone. Undoing the removal of a previously moved
container element therefore encodes `createdAt > movedAt`, and every peer and
the server would have rejected that change at decode time. Rule dropped; the
`removedAt` half, which no replica can produce, stays.

**A wire rejection is retroactive to everything already stored.**
`ErrInvalidElementTicket` and `ErrRefusedMember` were enforced on
`FromOperations`, which is also the DB read path (`ChangeInfo.ToChange`),
where only `ErrMissingTicket` was rescued -- so one stored change carrying
either shape would make its document permanently unloadable and fail every
client's pull. `normalize.go` now names the rescuable set in one predicate
(`rescuableStoredRejection`) that both stored entry points consult, so a
rejection added on the wire has one obvious place to be answered.

**Identity comparisons and proxies do not mix.** The new
`pair.parent == owner` guard compares against whatever
`RegisterRemovedElementPair` recorded, and the json layer recorded its own
proxy (`*json.Object`/`*json.Array`), never the `*crdt` container that
`Root.FindByCreatedAt` hands `Set.Execute`. The guard could not match on the
clone root, which is where local undo runs -- silently skipping the retire
this task added. The json layer now registers the embedded CRDT container,
for `RegisterElement` too: its `parent` reaches `adoptRemovedElementPair` and
would have recorded proxies the same way.

## The repro has to reach the check it claims to trip

Round 6 said an ArraySet redo would be refused at the wire. The first repro
passed on the unfixed code: `validateElementTickets` only runs for container
payloads, and the Go `json.Array` can only assign primitives, so a Go client
never carries the shape to the check. Tracing the redo change showed the
shape was there all along (removedAt 4, createdAt 7), just below the check's
reach. The test now asserts the invariant on the operation itself, which is
what fails for Go, and names the JS path (assigning an object) that reaches
the rejection. A test that passes before the fix is a question, not a Green.

## A wire rule applies to every reader of the decoder, not just the wire

The ticket rules lived in `FromOperations`, which the server's push handler,
every client's pull, the stored-change read and the snapshot path all share.
So each new rejection applied retroactively to data the server had already
accepted, and the review loop kept adding rescues for the readers it broke:
drop the operation on the stored path, sanitize it on the pull path. Dropping
an operation that applied on every replica is not a rescue -- the server's
replay diverges from the clients, and whatever later refers to it fails.

The rule belongs to the one boundary that takes untrusted input: the server
receiving a push (`FromPushedChangePack`). Every other reader decodes the way
it did before the rule existed. Before adding a check to a shared decoder,
list its callers and ask which of them can still tell the sender.

## Check a "cannot happen" against the other SDK

"No replica emits removedAt before createdAt" held for Go after the ArraySet
fix and not for the JS SDK, whose ArraySet reverse still keeps the tombstone.
A boundary rule has to accept whatever any shipped client sends, so the
premise is checked against every SDK's producer code, not the one being
changed.

## A guard keyed on a collision has to survive the collisions honest clients make

The live-slot refusal was argued safe because "a causal change log issues a
fresh createdAt per value". It does not: before attach, every client issues
tickets as InitialActorID from the same lamport, so two clients that set the
same key before attaching hand the server two different texts under one
createdAt. The refusal judged the second one forged, the edits that followed
it found the surviving text by createdAt, and the benchmark's document grew
until the job timed out. Concurrent undos restoring one value make the same
collision on purpose.

Before keying a check on identity, list how honest clients mint identities:
pre-attach tickets, undo restores, re-identification. A stateless rule over
the operation's own bytes can only judge the shape of one payload; whether an
identity is already taken needs the document, and the answer is only
meaningful once honest clients stop sharing identities (#2111).

## Validate the bytes, not what the decoder kept

The validator walked the decoded object, and the decoder had already
collapsed two members with one createdAt into one. The check never saw the
member it was meant to judge. Read the wire structure the decoder reads, in
the order it reads it, and check the decoded tree only for what survives
decoding.

## "One payload never reuses a ticket" was false, and a test said so

The panel's premise for payload-wide uniqueness was that a replica issues
every ticket once. True for tickets, false for payloads: undo re-identifies a
restored array element at its root only, so its descendants come back under
the tickets the tombstone still carries, and a copy of the array sends both.
The first version of the payload-wide rule rejected an ordinary
undo/redo history. Before turning "no replica can produce X" into a rule, run
the histories that copy state (undo, redo, restore of a container) through it,
not only the ones that mint fresh tickets.


## An exemption scoped to a subtree has to say which end it exempts

The array exemption was written as "elements of one array are judged apart
below their own level", and the code gave each element a fresh scope whose
siblings' identities were folded into the parent only after the loop. That
exempted more than the undo shape needs: a sibling's *own* createdAt was never
compared against another sibling's descendants, in either encoding order, so a
payload could hand two elements one identity by hiding one of them a level
down. Undo re-identifies a restored element precisely so its root differs from
everything the array holds -- only the descendants are shared. Scope the
exemption to the pair it was derived from (descendant vs descendant) and check
both directions explicitly; a scope chain alone encodes only one of them.

## A rule left "unjudged" on one operation is a rule on none

ArraySet's removedAt was left unjudged because its reverse can carry a value a
peer already removed, which made the Add rule ("a value never arrives removed")
reachable around: ArraySet.Execute runs the same InsertAfter, RegisterElement
and tombstone adoption. The legitimate shape was narrower than "anything":
executeUndoRedo re-identifies an ArraySet value with the undo's own ticket, so
removedAt always *precedes* createdAt. When a rule is about to be waived for an
operation, derive the exact shape that operation emits before waiving it -- the
waiver is the attacker's parameter otherwise.

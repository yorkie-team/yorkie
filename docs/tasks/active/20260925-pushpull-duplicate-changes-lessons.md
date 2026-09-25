# Lessons: filtering duplicate changes in PushPull

**Created**: 2026-09-25

## The repository already held the acceptance test

`server/packs/pushpull_test.go` carried the exact reproduction as a skipped
subtest, together with a `MockDB` whose only purpose is to fail
`UpdateClientInfoAfterPushPull`. Reading the test before designing anything
fixed the contract: after the retry, `DocInfo.ServerSeq` must not move and the
client's checkpoint must end at the seqs the first attempt reached. That is
stronger than "do not store the change twice" — it also requires the response
to *acknowledge* the already-stored changes, which is the half easy to miss.

## A dedup key has to survive identity reuse

`ClientSeq` is per-attachment, not per-actor: a detach/re-attach under the
same `StableActorID` restarts it at 1, so "this actor already has a change
with this `ClientSeq`" is not evidence of a duplicate. `Lamport` is what
carries across attachments, because the re-attaching client syncs its clock up
to the document before it edits. Neither field is a dedup key on its own; the
pair is.

The same reasoning kills the tempting unique index on
`(project_id, doc_id, actor_id, client_seq)` — it would reject legitimate
changes from a re-attached client.

## Pre-attach edits are the case that does not fit

A fresh attach seeds the checkpoint 0/0 and may carry local edits made before
the attach, at `ClientSeq` 1 / `Lamport` 1 — below whatever that actor stored
in an earlier attachment, and therefore indistinguishable from a re-send by
metadata alone. There is no signal in the pack that separates the two, so the
attach path is excluded by an explicit option rather than by a cleverer
predicate. Recognising that the ambiguity is real, not a gap in the predicate,
is what made the option the right answer instead of a workaround.

## Round-trip cost has a cheap necessary condition

Any duplicate must have been stored after the client was last acknowledged, so
`DocInfo.ServerSeq == cpBeforePush.ServerSeq` rules one out without a query.
In the single-writer steady state that is every push, so the lookup costs
nothing there; it is paid only when the document actually moved.

## The skipped test's payload never matched its own assertions

Un-skipping the reproduction was not enough: it pushed a change with no
operations and no presence, and asserted that change was "stored in the
database". The Mongo backend writes only changes that carry operations —
`CreateChangeInfos` routes presence-only changes to `presenceCache` and drops
an empty change entirely, keeping just the `server_seq` it consumed. So the
row the filter queries never existed, `FindChanges` returned nothing, and the
retry could not be recognised. A test that was skipped from the day it was
written has never had its fixtures checked against the storage it runs on;
re-reading the payload against `CreateChangeInfos` before trusting the
assertions is the step that was missing. The change now carries a `Set`
operation, which is what makes it durable.

## A metadata dedup key must be scoped to what the caller can prove

The first version of `filterStoredChanges` compared `ActorID`, `ClientSeq` and
`Lamport` — all three of which arrive verbatim from the wire. Nothing on the
push path proves a change belongs to the actor it names, so the filter could
be aimed at another client: forge rows under a victim's actor, raise the
stored `(ClientSeq, Lamport)` watermark, and the victim's genuine changes are
dropped *and* acknowledged. Gating on `ClientInfo.IsOwnActor` is what makes
the comparison a statement the server can stand behind, and it also caps the
work under the exclusive `DocPushKey` lock at one lookup per pack instead of
one per distinct actor in it.

## "A stored change always has lamport >= 1" is a Mongo-only fact

Using `latest.Lamport > 0` as the not-found signal read as harmless — Mongo
returns a zero-valued `ChangeInfo` when nothing matches. But the memory
backend inserts presence-only changes into `tblChanges`, and those carry
`Lamport` 0 (`ID.Next(true)`), so "found" and "not found" became
indistinguishable there. The signal has to be a field that is only ever set on
a real row: `latest.ActorID != ""`. The same lamport-0 shape on the *pushed*
side is worse — `info.Lamport <= latest.Lamport` holds for free — so
operation-less changes are excluded from the filter outright.

## Dropping a change from the write is only half of dropping it

`pushPack` filtered `pushables` but left the duplicate in `reqPack.Changes`,
and `pullSnapshot` replays that slice on top of a document already built
through `initialSeq`. Above the snapshot threshold the very operations the
filter exists to stop were applied twice into the response snapshot. A filter
that removes work from one consumer has to be checked against every other
consumer of the same input.

## A dropped duplicate may be the only chance to announce a stored change

The attempt that stored the change returned early, before the publish block,
so no `DocChanged` event, webhook or snapshot trigger ever fired for it. With
the retry dropping it, `len(pushedChanges) > 0` was false and the edit stayed
durable but unannounced forever. `pushPack` now returns the dropped changes
separately so `PushPull` can publish for them.

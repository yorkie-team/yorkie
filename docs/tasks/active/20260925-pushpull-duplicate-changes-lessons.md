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

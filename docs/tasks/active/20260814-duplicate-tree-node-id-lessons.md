# Lessons: two nodes sharing one TreeNodeID

**Created**: 2026-08-14

## A panic in a handler reaches the client as a bare 503

`net/http` resets the HTTP/2 stream when a handler panics, so the
gateway logs `503 UR upstream_reset_before_response_started
{remote_reset}` and the client sees a 503 with no gRPC status and no
error message. The panic itself is only in the serving pod's stderr, and
`kubectl logs deploy/yorkie` reads one pod — the panic was invisible
until the specific pod behind the failing requests was read directly.

The compaction housekeeping made the same call every 60 seconds, which
turned a rare user-visible failure into a signal that was easy to find:
1278 identical warnings in 24 hours, all for one document.

## Replay the stored history to find which change breaks

Rebuilding the document offline from its snapshot and changes —
`NewInternalDocumentFromSnapshot`, `ChangeInfo.ToChange`,
`ApplyChanges` one change at a time — pinned the failure to a single
change and, more usefully, to a single difference: replaying the full
history applied every change cleanly, and replaying from the snapshot
panicked. That contrast is what identified the snapshot round trip as
the trigger rather than the change itself.

## An invariant that is only "usually" true is not an invariant

`NodeMapByID.Put` assumes an id names one node. Nothing enforced it, and
two separate paths break it:

- an undo that re-inserts a deep copy of a removed node keeps its id
- the delimiters an element split consumes are simulated rather than
  replayed, so two nodes in one change can be issued the same ticket

The first is a client bug; the second is in this repo and still open.
A first version of the fix treated every collision as corruption and
broke `TestTree/edit its content with path when multi tree nodes
passed` — the integration suite caught what the unit tests could not,
because the collision only appears when an element split and an insert
share a change.

## Fail-closed is the wrong default for history

Refusing to apply a malformed change protects the tree, but the change
is already in the history of existing documents, and a change the server
cannot replay is a document that can never be loaded. Measured on the
real data, rejecting moved the failure from a panic to a hard error at a
later change — the document stayed unopenable either way. Dropping the
duplicated content and applying the rest keeps every document loadable,
at the cost of an undo that no longer restores its text.

## Verify a CRDT fix against the data that broke

The unit tests said the fix worked; the production snapshot and changes
said whether the document that motivated it would open. Replaying all
three real datasets (the broken document from its snapshot, its full
history, and the document from the original issue) after each iteration
caught that the reject-based version regressed the very case being
fixed.

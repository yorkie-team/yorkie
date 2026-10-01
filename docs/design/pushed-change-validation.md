---
title: pushed-change-validation
target-version: 0.7.6
---

# Pushed Change Validation

## Problem

An element payload on the wire carries `createdAt`, `movedAt` and `removedAt`
as three independently decoded tickets. Nothing between the wire and the CRDT
makes them agree with each other or with the operation's `executedAt`:
`Change.SetActor` rewrites only the operation's own `executedAt`. A client can
therefore push a triple no replica could have issued, and the CRDT has to
survive it.

Two shapes matter:

- **An un-tombstonable member.** `Element.Remove` and
  `ElementRHT.DeleteByCreatedAt` both refuse a ticket that does not follow the
  element's `createdAt`. A member whose `removedAt` does not follow its
  `createdAt`, or whose `movedAt` precedes it, can lose its key to a later
  `Set` without being tombstoned: it stays live in `nodeMapByCreatedAt`,
  unreachable by key, emitted into every later snapshot, charged to
  `docSize.Live` and collectable by nothing.
- **A hijacked identity.** `Root.elementMap` is keyed by `createdAt` for the
  whole document, and `RegisterElement` hands the slot to whatever registered
  last. A value carrying the `createdAt` of a live element in *any* container
  strands that element: every later operation addressed at that `createdAt`,
  and every collection and snapshot that resolves one, finds the pushed value
  instead — on every replica, and in the server's own change-log replay.

## Goals

- Keep both shapes out of a document, at every entry point a client reaches.
- Keep every *other* reader lenient, so data written before a rule existed
  stays readable.
- Leave a client that trips a rule a way back to a working document.

## Non-Goals

- Rejecting shapes replicas really emit. Undo re-identifies the value of an
  `Add` or an `ArraySet` reverse with a freshly issued `createdAt` while the
  copy keeps its older `movedAt`, and the JS SDK's `ArraySet` reverse also
  keeps an older `removedAt`. Documents already hold both, nested in
  containers.
- Repairing a document that already holds one of these shapes.

## Design

Enforcement is split by what each layer can decide safely.

### The wire: strict on push, lenient everywhere else

`converter.FromPushedChangePack` decodes a pack like `FromChangePack` and then
runs `ValidatePushedOperations` over it. The rules are the ticket rules above,
applied to object members of a `Set`, `Add` or `ArraySet` payload, plus a type
check on an `Increase` value. Array elements are exempt from the ticket rules,
for the reason under Non-Goals.

Every other reader — a client pulling, the server reading a stored change or a
snapshot — keeps using `FromChangePack` and the lenient decoders, which drop a
member the `ElementRHT` refuses rather than failing. Those readers hold changes
the server already accepted: failing to read one makes the document unloadable,
and dropping it makes the reader diverge from every replica that applied it.

### The repair path

The asymmetry has a cost: a document can already hold a shape the push rules
refuse, and a client does not discard a change a push rejected — it resends the
same pack forever. `converter.FromLeavingChangePack` is the way out.
`DetachDocument` and `RemoveDocument` decode with it: a pack that breaks a rule
leaves with its `Changes` dropped instead of failing the request, so the client
can always get out of the document and back in with a clean copy built from the
server's snapshot. `AttachDocument` and `PushPullChanges` keep the hard
rejection, because applying such a pack is what the rules exist to prevent.

### Execution: refuse the payload, not the pack

A rule the wire cannot express is enforced where the state is, so that every
replica and the server's snapshot replay decide the same way from the same
state:

| Guard | Where | On refusal |
|-------|-------|------------|
| A loser whose `createdAt` a live node of the same object holds, or that cannot be tombstoned at all | `ElementRHT.SetWithExecutedAt` | Not indexed; reports `indexed == false` so the caller books nothing |
| A value whose `createdAt` a live element *anywhere* in the document holds | `operations.hijacksLiveElement`, consulted by `Set`, `Add` and `ArraySet` before they mutate | The operation is a no-op: no reverse, not observable |
| An `Increase` value that is not a numeric primitive | `Increase.Execute` | `ErrNotApplicableDataType` for a local or undo/redo apply; a no-op for a remote apply or a replay |

Only a *live* occupant is protected. A tombstone leaves `elementMap` when it is
purged, and each replica and the server collect on their own schedule, so a
guard that also fired on a tombstone would skip the operation on a replica
still holding it and apply it on one that had collected it. A live element is
never purged, so every replica that has it decides alike. This is also why
refusing converges where failing the pack would not: refusing leaves the
document byte-identical, which is the correct idempotent outcome for the one
benign cause — a re-applied change.

## Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A false positive wedges a client permanently | `FromLeavingChangePack` keeps detach and remove available, so the rejection is a setback rather than a dead end |
| The push path decodes each element payload twice (once leniently for the pack, once strictly for validation) | Bounded by the payload the client already sent; the cost falls only on the four push endpoints |
| An execute-time refusal silently drops a legitimate operation | The guards fire only on an identity a live element already holds, which a causal change log cannot produce |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

---
title: pushed-payload-validation
target-version: 0.7.6
---

# Pushed Payload Validation

## Problem

An element payload carries `createdAt`, `movedAt` and `removedAt` as three
tickets decoded from client bytes, independently of each other and of the
operation's `executedAt`. Nothing between the wire and the CRDT makes them
agree: `Change.SetActor` rewrites only the operation's own `executedAt`. A
crafted Set, Add or ArraySet can therefore carry a value or nested member that
no replica could have produced:

- a member that `ElementRHT` can neither index by key nor tombstone, which
  stays live, unreachable, charged to `docSize.Live` and emitted into every
  later snapshot;
- two members of one object under one `createdAt`, which collapse into one
  `nodeMapByCreatedAt` entry on decode. One of them is never seen by any walk
  of the decoded object, and the decoded copy answers to both keys while the
  server's snapshot carries only one, so clients that attach later diverge
  from clients that applied the change;
- an Add value that arrives removed, which goes straight into
  `gcElementPairMap` under a `createdAt` of the sender's choosing.

### Goals

- Reject, at the point the server takes a client's changes, payloads that no
  replica can produce.
- Never reject a payload a replica can produce. A rejected change stays at the
  head of the client's push queue, so a false positive wedges the client.
- Treat Set, Add and ArraySet the same way: every operation that carries an
  element payload goes through the same rules, with exemptions only where a
  replica really emits the shape.

### Non-Goals

- A `createdAt` that collides with an element already in the document. See
  Design Decisions.
- Changing what any replica does when it applies an operation. The CRDT side
  of the same family of bugs is
  [#2100](https://github.com/yorkie-team/yorkie/pull/2100).
- A repair path for a client that already holds a rejected change.

## Design

`converter.FromPushedChangePack` runs `ValidatePushedOperations` on every
change of the pack, then decodes it with `FromChangePack`. The four RPCs that
take a client's changes use it: `AttachDocument`, `DetachDocument`,
`PushPullChanges` and `RemoveDocument`. They log a refusal with the client and
document before returning `InvalidArgument`.

Every other reader -- a client pulling, the server reading a stored change or a
snapshot -- keeps the lenient decoders. Those bytes were already accepted, and
data written before a rule existed has no other source: failing to read it
makes the document unloadable, and dropping it makes the reader diverge from
every replica that applied it.

The validator is stateless. It reads the operation's protobuf and nothing of
the document, and it reads tickets from the same bytes `fromElement` builds
the value from: a container that carries its subtree is read from the subtree,
anything else from the simple element's `createdAt`.

### Rules on the value of an operation

| Rule | Set | Add | ArraySet |
|---|---|---|---|
| `createdAt` does not follow `executedAt` | yes | yes | yes |
| `removedAt` follows `createdAt` | yes | -- | -- |
| no `removedAt` | -- | yes | -- |

- Both SDKs issue one ticket for a fresh value and its operation. Undo
  restores an older value under a newer ticket (Set), or re-identifies the
  value with the undo's own ticket (Add, ArraySet; `executeUndoRedo` in Go,
  `document.ts` in JS). A value created before attach keeps `InitialActorID`,
  which orders before every real actor at the same lamport.
- The JS Remove reverse can restore a key's tombstone, so a removed Set value
  is legitimate. One removed before it was created is not.
- Both SDKs build the Add that undoes an array Remove from the target before
  deleting it, and skip the undo when the target is already gone. An Add never
  carries a tombstone.
- The JS ArraySet reverse copies a displaced value a peer already removed, and
  undo re-identifies the copy with a newer `createdAt`. An ArraySet value's
  `removedAt` is therefore not judged.

### Rules on object members nested in the value

The members of each object are replayed in the order `fromJSONObject` feeds
them to `ElementRHT.SetWithExecutedAt`.

- `removedAt` follows `createdAt`.
- `movedAt` does not precede `createdAt`. `ElementRHT` anchors both the LWW
  comparison and the eviction on `PositionedAt`, so a member positioned before
  its creation loses its key without being tombstoned.
- No two members share a `createdAt`. `Nodes()` reads `nodeMapByCreatedAt` in
  both SDKs, so the encoder emits one node per `createdAt`.
- A member that loses its key to one replayed before it carries a `removedAt`,
  or the winner's `positionedAt` follows its `createdAt`. Otherwise
  `Element.Remove` refuses the winner's ticket.

Array elements are exempt from the ticket rules. Undo re-identifies an Add or
ArraySet value with a fresh `createdAt` while the copy keeps its older
`movedAt`, and the JS ArraySet reverse also keeps an older `removedAt`, so
documents already hold both shapes inside arrays and restored containers carry
them. An object nested in an array is still checked.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A rule rejects a shape some client emits, wedging it | Every rule is derived from what Go and JS emit, including undo/redo and pre-attach tickets. `TestPushBoundaryAcceptsReplicaHistories` drives those histories through `FromPushedChangePack`. Refusals are logged, so a false positive is visible |
| Mobile SDKs (iOS, Android) emit a shape the Go and JS SDKs do not | Not verified here. The rules only constrain a value against its own operation and members against their own object, which any SDK built on the same CRDT satisfies |
| Element `RestoreMode` (revive by identity) ships on Set/Add | The wire field exists but no SDK emits it for elements yet. The value rules have to be revisited with it |
| A second protobuf unmarshal per container payload on every push | No CRDT is built for validation; the cost is one `proto.Unmarshal` of the bytes the decoder reads anyway |

### Design Decisions

| Decision | Reason |
|----------|--------|
| No `createdAt`-collision check against the document | It needs the document, and legitimate histories produce the collision today. Elements created before attach share the initial actor's tickets across clients, and two replicas undoing concurrent overwrites restore one value under one `createdAt`. A refusal keyed on "a live element already has this `createdAt`" fired on the first of these in `BenchmarkRPC/attach large document`: the losing text was refused, both clients' edits landed on the surviving one, and the document grew past 16 MB until the job timed out |
| No replicated refusal in `Set.Execute`, `Add.Execute` or `ArraySet.Execute` | A refusal that runs on apply exists only in Go. The JS SDK applies the operation, and the server's snapshot replay runs the Go guard, so any history the guard misjudges splits the server snapshot from JS clients. An earlier guard on every registration (e9fb7f52, reverted) split Go replicas in `TestConcurrentUndoRestoresSameValue` |
| Validate at the push boundary only | Readers of accepted data must stay lenient; see Design |
| Validate protobuf, not the decoded tree | Members that collapse on decode are invisible in the decoded tree |
| Exempt array elements from the ticket rules | Replicas emit those shapes today |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Refuse a value whose `createdAt` a live element holds, in `Root` or each `Execute` | Fires on legitimate histories (pre-attach collisions, concurrent undo restores) and diverges from the JS SDK and the server's replay. Revisit once [#2111](https://github.com/yorkie-team/yorkie/pull/2111) re-issues pre-attach tickets and the JS SDK carries the same rule |
| Check the collision on the server against the stored document at push time | Needs the document loaded on every push, and the same legitimate collisions apply |
| Require an Add/ArraySet value's `createdAt` to equal `executedAt` | Holds for Go and JS, but `executedAt` is client-chosen too, so it adds no protection against a forged identity while raising the wedge risk for other SDKs |
| Validate in the lenient decoders and drop what fails | Diverges every reader from the replicas that applied the change, and can make a document unloadable |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

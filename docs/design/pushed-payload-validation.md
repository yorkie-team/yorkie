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
- two elements of one payload under one `createdAt`, or one under the
  document root's `time.InitialTicket`. `Root.elementMap` keeps one element
  per `createdAt`, so the other becomes unaddressable; inside one object the
  two also collapse on decode, so the decoded copy answers to both keys while
  the server's snapshot carries one;
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
- Pushing a rejected change later. A client holding one can still detach or
  remove the document (see Design), but the change itself is never accepted.

## Design

`converter.FromPushedChangePack` runs `ValidatePushedOperations` on every
change of the pack, then decodes it with `FromChangePack`. The four RPCs that
take a client's changes use it: `AttachDocument`, `DetachDocument`,
`PushPullChanges` and `RemoveDocument`. They log a refusal with the client and
document. Attach and PushPull then return `InvalidArgument`.

Detach and Remove do not fail on a refused payload. Otherwise a client holding
a rejected change could neither push it nor leave the document. They decode the
pack leniently, authorize it as sent (changes included, so the leave is not
judged as a read), and then drop its changes, so nothing of them reaches the
document. A detach or remove over the size limit drops its changes the same way
(`packs.PushPull`).

An Increase carries a delta, not a document element. Its value must be a
primitive: both SDKs send a number, and `Increase.Execute` drops anything else
on every replica.

Every other reader -- a client pulling, the server reading a stored change or a
snapshot -- keeps the lenient decoders. Those bytes were already accepted, and
data written before a rule existed has no other source: failing to read it
makes the document unloadable, and dropping it makes the reader diverge from
every replica that applied it.

The validator is stateless. It reads the operation's protobuf and nothing of
the document, and it reads tickets from the same bytes `fromElement` builds
the value from: a container that carries its subtree is read from the subtree,
anything else from the simple element's `createdAt`.

### Rules from the change's own ID

The value rules below compare two tickets the sender picked, so they bound
nothing on their own. `ValidatePushedChange` binds them to the change carrying
them first:

- Every operation's `executedAt` is the change's actor's, at a lamport no
  greater than the change's own. `Context.IssueTimeTicket` stamps every
  operation of a change from that change's ID, and `Change.SetActor` rewrites
  the actor of both together.
- No ticket anywhere in a payload runs ahead of the change's lamport. A pushed
  payload is a copy of what the sender's replica holds, and every ticket in a
  replica was issued by a change the sender had already applied. Without this
  an attacker picks `MaxLamport`: a `createdAt` there poisons an object key no
  later Set can win back, and a `removedAt` there is a tombstone no version
  vector ever passes, so it is charged to the document's size forever.

This bounds a ticket's lamport, not its actor. A payload legitimately carries
other replicas' tickets, and which actors exist is not knowable from the
operation's own bytes; the actor a *change* is stamped with is bound separately,
against the authenticated client, in `packs.validateChangeActors`.

### Rules on the value of an operation

| Rule | Set | Add | ArraySet |
|---|---|---|---|
| `createdAt` does not follow `executedAt` | yes | yes | yes |
| `removedAt` follows `createdAt` | yes | -- | -- |
| no `removedAt` | -- | yes | -- |
| `removedAt` precedes `createdAt` | -- | -- | yes |

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
- The JS ArraySet reverse copies a displaced value a peer already removed, so
  an ArraySet value can arrive removed. It only reaches a push through undo,
  which re-identifies the copy with the undo's own fresh ticket
  (`executeUndoRedo`), so its `removedAt` always precedes its `createdAt`.
  `ArraySet.Execute` inserts and `RegisterElement`s its value exactly as
  `Add.Execute` does, so a tombstone arriving under a `createdAt` no older
  than its removal -- the shape the Add rule refuses -- is refused here too,
  rather than left unjudged.

### Rules on object members nested in the value

The members of each object are replayed in the order `fromJSONObject` feeds
them to `ElementRHT.SetWithExecutedAt`.

- `removedAt` follows `createdAt`.
- `movedAt` does not precede `createdAt`. `ElementRHT` anchors both the LWW
  comparison and the eviction on `PositionedAt`, so a member positioned before
  its creation loses its key without being tombstoned.
- A member that loses its key to one replayed before it carries a `removedAt`,
  or the winner's `positionedAt` follows its `createdAt`. Otherwise
  `Element.Remove` refuses the winner's ticket.

No replica can have built a member that breaks these, so they hold for an
undo copy of an existing document as well. `Element.Remove` refuses a
`removedAt` that does not follow `createdAt` in both SDKs and has since 2022,
`ElementRHT` stamps a winning member's `movedAt` with an `executedAt` no older
than the value, and a live loser left by older replicas (before losers were
removed by their own state) can still be tombstoned.

### Identities across the whole value

- The scope is one operation's value, not the whole change. Operations of one
  change legitimately carry one element twice: a Set holds a live reference to
  its value, so a change that creates an object and then fills it encodes the
  filled subtree into the first Set and each member again into its own
  (`TestPushBoundaryAcceptsReplicaHistories` covers it). A collision an
  attacker splits across two operations is therefore out of reach here, for
  the same reason the document-wide collision is -- see the Non-Goals.
- No element of the value reuses a `createdAt`: not its root, not an object
  member at any depth, not an array element. `Root` registers every element of
  an applied value in one document-wide `elementMap` keyed by `createdAt`, so
  two elements under one ticket leave one unaddressable (never removable,
  charged to Live, re-emitted into every snapshot) and collapse into one
  `gcElementPairMap` entry once both are removed. Within one object the
  encoder cannot even emit two, since `Nodes()` reads `nodeMapByCreatedAt` in
  both SDKs.
- The exception is below the elements of one array. Undo restores a removed
  array element as a deep copy re-identified at its root only, in both SDKs,
  so the array holds the tombstone and the live copy and their descendants
  share every `createdAt`. Two replicas undoing concurrent removals of one
  element leave two live copies the same way, and any copy of an enclosing
  value sends them together. The exemption is between *descendants* only: an
  array element's own `createdAt` is checked against the descendants of its
  siblings as well as against their roots, in both encoding orders, since
  undo re-identifies a restored element precisely so that it differs from
  everything the array already holds. Nothing in an array may reuse an
  identity from outside it either. The
  `RestoreMode` comment in `resources.proto` describes the same duplication.
- No element is created at lamport 0. No replica issues such a ticket (a
  client's first change is lamport 1), and the document root lives at
  `time.InitialTicket`: a value claiming it would take over the root's
  `elementMap` slot and capture every later root-level operation.
- The position identities of one array are distinct, and none is at lamport 0.
  `fromJSONArray` feeds `position_created_at` to `AddDeadPosition` and
  `AddMovedElement`, which key `RGATreeList.nodeMapByCreatedAt` by it; an
  element that was never moved is keyed by its own `createdAt`. That map is
  what every insert-after and move resolves against, so two nodes under one key
  leave one of them unaddressable, and lamport 0 is the array's dummy head's
  own slot. These are a per-array namespace, not element identities: a moved
  element's abandoned position legitimately keeps the element's own
  `createdAt` (`RGATreeList.MoveAfter`), so they are claimed apart.

A tree value is read from its bytes like a container, since `BytesToTree` takes
its `createdAt`, `movedAt` and `removedAt` from there.

Array elements are exempt from the ticket rules. Undo re-identifies an Add or
ArraySet value with a fresh `createdAt` while the copy keeps its older
`movedAt`, and the JS ArraySet reverse also keeps an older `removedAt`, so
documents already hold both shapes inside arrays and restored containers carry
them. An object nested in an array is still checked.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A rule rejects a shape some client emits, wedging it | Every rule is derived from what Go and JS emit, including undo/redo and pre-attach tickets. `TestPushBoundaryAcceptsReplicaHistories` drives those histories through `FromPushedChangePack`. Refusals are logged, so a false positive is visible |
| Mobile SDKs (iOS, Android) emit a shape the Go and JS SDKs do not | **Open.** Every rule here was derived by reading the Go and JS SDKs; the iOS and Android SDKs were not read. The rules only constrain a value against its own operation and members against their own object, which any SDK built on the same CRDT satisfies, but that is an argument, not a verification. The failure mode if it is wrong is a wedged client, so before this ships: (a) read the two mobile SDKs' undo/redo and element-copy paths for the shapes the rules judge, or (b) run the gate in log-only mode for a release -- every refusal already logs the client and document it came from (`fromPushedChangePack`) -- and ship the refusal once the logs are quiet. Until one of those is done, an affected client can still leave the document (the Detach/Remove leniency below), so the wedge is escapable by detaching and re-attaching, at the cost of its unpushed local changes |
| Element `RestoreMode` (revive by identity) ships on Set/Add | The wire field exists but no SDK emits it for elements yet. The value rules have to be revisited with it |
| A document already corrupted by pre-attach collisions on `main` holds two elements under one `createdAt` outside an array, and an undo copies them back | Refused; the document's identity resolution is already broken there. [#2111](https://github.com/yorkie-team/yorkie/pull/2111) removes the source by re-issuing pre-attach tickets |
| A document crafted before this gate holds a member that breaks the member rules, and an honest undo copies it back into a Set | Refused, and the client that pushes it is wedged. The shape has one source -- a crafted push, which this gate closes going forward -- and the document it sits in is already broken: the member is unreachable by key and uncollectable. Accepting it to keep that one client moving would reopen the rule for every sender. A repair path for a client holding a rejected change is an explicit Non-Goal above, and refusals are logged so such a client is visible rather than silent |
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

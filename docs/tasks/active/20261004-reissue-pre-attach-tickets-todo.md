**Created**: 2026-10-04

# Re-issue pre-attach tickets to the real actor on attach

A document edited before `Client.Attach` mints every ticket under
`time.InitialActorID`. `InternalDocument.SetActor` rewrites only each
operation's `executedAt` (its own TODO says the root is not updated), so
an element's `createdAt` keeps the initial actor. Two clients that fill
the same key before attaching push values with identical `createdAt`.

On `main` this already loses data: three rounds of
`SetNewText("k1").Edit(0, 0, ...)` + attach on two clients end with the
root `{}`. On #2081/#2100 the new "refuse a Set whose value's createdAt
names a live element" rule fires on this legitimate collision, the
same-change Edits pile onto the live element, the document passes
20 MB, compaction fails and logs the whole root on one line, and the
bench job (`BenchmarkRPC/attach_large_document`) hangs for 6 h.

Maintainer decision: on attach, re-issue those tickets to the real actor
so `createdAt` is unique per client. Design:
`docs/design/pre-attach-ticket-reissue.md`.

## Where a pre-attach ticket lives

- Local changes: change ID actor and version vector key; every
  operation's `executedAt`, `parentCreatedAt`, `prevCreatedAt`,
  `createdAt`; Set/Add/ArraySet values (and the elements nested inside an
  Object/Array/Tree value); Edit/Style positions; TreeEdit positions,
  contents, split tickets and restore spans.
- The root: `elementMap` keyed by `createdAt`, the GC maps, every
  element's created/moved/removed tickets, text and tree node IDs and
  their indexes, attribute `updatedAt`s.
- Presence map keyed by actor; `changeID` actor and version vector.
- Undo/redo stacks (reverse operations name the old tickets).
- Not on the server: a document that never synced has pushed nothing.

## Plan

- [x] Red unit test (`pkg/document`): after `ReissueActor`, no ticket in
      the root snapshot or in the local changes names the initial actor
      (except the lamport-0 root ticket), and the local root equals the
      root a server builds from the pushed changes.
- [x] Red unit test: two documents fill the same key before attach,
      re-issue to different actors, cross-apply -> converge to the LWW
      winner, not `{}`.
- [x] Red integration test: two clients fill the same key before attach
      and converge to a non-empty LWW result; a third client sees it.
- [x] `converter.ReissueOperations`: wire round trip with a generic
      protoreflect walk over every `TimeTicket` (nested element bytes
      decoded and re-encoded).
- [x] `InternalDocument.ReissueActor`: only for a never-synced document
      (initial checkpoint, version vector naming no other actor, local
      changes present). Re-issue the local changes, rebuild root and
      presences by replaying them on a fresh root, re-key the version
      vector. Atomic: assign only after every step succeeded.
- [x] `Document.ReissueActor`: lock, invalidate the clone, clear the
      undo/redo stacks when tickets were re-issued.
- [x] `Client.Attach`: call `Document.ReissueActor` for documents instead
      of the plain `SetActor`.
- [x] ~~Separate commit: compaction failure logs root size, not the
      whole root~~ -- landed on `main` in #2108 while this was in flight.
- [x] Design doc + README entry.
- [x] Verify: `GOTOOLCHAIN=go1.26.0 make lint`, `go test ./...`,
      targeted integration, `BenchmarkRPC/attach_large_document
      -benchtime 10x`, `make verify`.
- [x] PR to `main`, `@claude loop` comment.

## JS follow-up (yorkie-js-sdk)

`Document.setActor` in `packages/sdk/src/document/document.ts` has the
same TODO and rewrites `executedAt` only (TreeEdit also re-stamps its
split tickets). A JS client that edits before attach pushes the same
colliding `createdAt`. The JS port needs the same never-synced guard,
ticket re-issue of pending changes, root/presence rebuild by replay and
history clear. The offline-persistence restore path (`restoreFromBytes`
after `setActor` with a stable actor) is unaffected: the persisted actor
equals the stamped one, so nothing is re-issued.

**Created**: 2026-09-26

# Lessons: tree anchor and clone reset

- yorkie-js-sdk#1394 was written against yorkie#2031, which was closed
  unmerged. Mirroring a server PR starts with checking that it merged and
  reading the merged code; four agent fix rounds went into a target that did
  not exist on `main`.
- A clone/root divergence is reproducible without mocks: a remote change
  whose second operation names a missing parent fails on the clone after its
  first operation ran, and `Root()` (clone) and `Marshal()` (root) then
  disagree.
- Taking `d.mu` on a read path is only safe once nothing holds that lock
  across a blocking send. `applyChanges` published every event it produced
  while still inside `ApplyChangePack`'s lock, on a channel of capacity one,
  so the moment `Root()` started locking, an application slow to drain events
  could wedge every read. The fix is a second mutex, `eventsMu`, taken
  *before* `d.mu` and held across "mutate, then publish": ordering (yorkie#1847)
  survives, the send happens with `d.mu` released.
- Invalidating a clone by storing `nil` only works while every reader is
  serialized. `d.updating` is per-document, not per-goroutine, so a reader
  racing another goroutine's updater still ran unlocked and could see that
  `nil` between its `ensureClone` and its dereference. Marking the clone
  stale and leaving the pointer live removes the nil entirely: the worst an
  unlocked reader sees is a clone one rebuild out of date, which is what it
  saw before the lock existed.
- Copying a change pack's slice is not enough to hand it to another
  goroutine. `Context.NextID` returns the very `ID` the `Change` carries, so
  each buffered local change's version vector is the *same map* as
  `d.changeID`'s, and `SyncClocks`/`SetClocks` call `VersionVector.Max` on it
  in place while a remote pack applies. `CreateChangePack` now rebuilds each
  change with a deep-copied vector; the pack is only safe to serialize after
  the lock drops once it owns every map it exposes.
- `inner.Map.DeepCopy` is copy-on-write at the *map* level only: the clone
  and the root share the same `Presence` values. `LoadOrStore` therefore
  handed the updater the root's own map, so `presence.Set` wrote through and
  no clone reset could undo it. Re-`Store`ing the value gives the clone an
  owned copy, and the root takes the presence only when `Change.Execute`
  applies it -- which is also what makes the `DisablePresence` drop path
  need an `invalidateClone`.
- A lock added to a setter needs the same `d.updating` escape the readers
  got. `SetActor` and `SetStatus` are reachable from inside an updater, and
  `sync.RWMutex` is not reentrant, so a bare `Lock()` there deadlocks the
  process rather than merely racing. `writeLocked` is `readLocked`'s
  counterpart for exactly that. (Superseded: measurement later showed the
  setters are only called beside an updater, so they lock unconditionally
  and `writeLocked` was removed.)
- `inner.Presence.Clear` had a pointer receiver and assigned a fresh map
  (`*p = make(...)`), so `proxy.Presence.Clear`'s `data := p.data;
  data.Clear()` rebound only its own copy of the map header. `p.data` -- the
  clone-owned map this branch introduced -- kept every key, so a `Set` after
  a `Clear` re-broadcast the presence the user had just cleared. A map value
  handed around by header has to be emptied in place (`clear`), never
  reassigned through a pointer: the reassignment is invisible to every other
  holder.
- The `d.updating` escape is not free to apply to every reader. It is
  per-document, so putting `HasLocalChanges` on it handed the client's sync
  loop (`Attachment.needSync`) an unlocked read of the slice `Update`
  appends to on the application goroutine. The escape only pays for itself
  where a call can actually come from inside an updater; where it cannot,
  the bare lock is both correct and what the caller wants.
- That last trade was wrong for an *exported* reader, and the panel was
  right to call it. `HasLocalChanges` is public API, so "no caller reaches
  this from inside an updater" is a statement about today's callers, not a
  guarantee -- and the failure mode of being wrong is a deadlocked process,
  not a racy read. The per-document escape's hole is shared by every
  accessor (`Marshal`, `CreateChangePack`); closing it for one method buys
  nothing and costs reentrancy. It is back on `readLocked`. (Superseded
  by the measurement below: `HasLocalChanges` takes the bare lock, as it
  did on `main`.)
- The same map-header-rebinding bug as `Clear` lived in
  `Presence.Initialize` (`p.data = data`), where it is quieter: the change
  payload carries the new map, so the root looks right, and only the *next*
  `Set` -- built from the clone that never saw the swap -- silently drops
  the initialized keys. Fixing one instance of a hazard is worth a grep for
  the rest of them.
- A fix to state the user cannot read back directly still needs a test. The
  clone survives a successful `Update`, so the assertion that catches both
  bugs is on the presence *after the following Update's `Set`*, not right
  after the `Clear`/`Initialize` (`presence_clone_test.go`).
- The `HasLocalChanges` back-and-forth above was settled by measurement, not
  argument. Instrumenting the escape and running the unit and integration
  suites showed exactly which accessors are reached from inside an updater:
  `Root`, `RootObject`, `ActorID` and `GarbageCollect`. The client's sync
  and attach paths (`HasLocalChanges`, `CreateChangePack`, `SetMaxSizeLimit`,
  `SetSchemaRules`, `ResetPresences`) are only ever called beside one, so
  they lock unconditionally. `HasLocalChanges` had done that on `main`, so
  the escape there was a regression, not a trade: a `-race` test with one
  goroutine in `Update` and another calling it passes on `main` and fails on
  the escape.
- When a review loop flips a decision twice, stop weighing the two
  arguments and collect the data that decides between them.

## Panel round: blast radius, security and correctness

- The `DisablePresence` branch of `Update` was the only invalidation point
  that marked the clone stale and then *kept running*. Every other one
  returns on the next line, so the "stale clone" window is empty; this one
  spanned schema validation, the size check and `Change.Execute`, during
  which a concurrent reader taking the `d.updating` escape would
  `DeepCopy` the live root mid-execution. `defer d.invalidateClone()` --
  which, by LIFO, still runs under `d.mu` and before `updating` is
  lowered -- makes it look like the others.
- A comment is not enforcement. `Document.ResetPresences` documented that
  callers must not reach the unlocked map through `InternalDocument()`,
  and nothing stopped them; production had zero such callers, so the
  contract cost nothing to make structural. `InternalDocumentForTest` is
  the whole fix: the next production caller has to type the suffix.
- The watch loop handled the stream's first response *before* starting the
  pump that drains the document's event channel. With the publish now
  under `eventsMu` rather than `d.mu`, a re-established stream could park
  a publisher on a capacity-one channel with no consumer while the reader
  goroutine was itself blocked on `eventsMu` -- the restart never reached
  the line that creates the pump. Starting the pump first costs nothing
  and removes the window.
- A producer-side guard on a value that crosses the wire is half a fix.
  `leftAnchorID`'s empty-text floor only corrects the anchor *this*
  replica computes; peers without it, and spans already stored, still
  deliver the negative offset. The decoder floors rather than rejects,
  because `FromStoredOperations` shares it and a rejection would make an
  affected document unrebuildable.
- The server-side half of `MaxSizePerDocument` is a known, written-down
  gap (`docs/design/document-size-limit.md`, status: proposal), not
  something this branch introduced: it only moved the client-side write
  behind a locked setter. Rebutted rather than fixed.

## Panel round: the floor's blast radius

- A coercion is only safe where the value is *resolved*, never where it is
  *keyed*. Flooring the negative anchor inside `fromTreeNodeID` fixed the
  restore span and silently rewrote every other `TreeNodeID` the same
  decoder produces -- a node's own `Id`, `InsPrevID`/`InsNextID`/
  `MergedFrom`, both halves of a `TreePos` -- where `(createdAt, offset)`
  *is* the identity, so offset `-1` and offset `0` would decode onto one
  node. Split the decoder: `fromTreeRestoreAnchorID` floors (an anchor is
  resolved by position, and `FromStoredOperations` must stay able to
  rebuild a document already holding one), `fromTreeNodeID` rejects with
  `ErrInvalidTreeNodeID`. Nothing legitimate ever wrote the others
  negative -- `leftAnchorID` is the sole producer, and it only feeds a
  span's left sibling -- so the rejection has no stored population to
  strand. (Reverted in the maintainer round below.)

## Maintainer round

- "No legitimate producer" is not "no stored population". Before this
  branch nothing validated the offset, so any client could have pushed a
  negative identity offset, and the rejection sat on a decoder that
  stored changes and snapshots share. One such value would have made the
  document undecodable for every reader. The rejection was new to this
  branch and belonged in a follow-up, so it was dropped rather than
  patched: `fromTreeNodeID` passes the offset through as on `main`, and a
  test pins that `FromStoredOperations` still decodes one.
- A review loop that runs long without converging keeps adding scope. Every
  round's fixes grew the diff the next round had to review. When it stalls,
  sort the open findings by where they came from: introduced here, fix
  them; already on `main`, file them.

## Panel round (post-maintainer)

- Reverting the identity rejection wholesale also un-guarded the IDs the
  restore span itself is keyed by (`Id`, `ParentId`, `RightSiblingId`),
  which reach `Tree.Restore`'s recreate path bounded nowhere downstream.
  The stored-population argument does not cover those: no producer ever
  writes them negative, so `fromTreeRestoreIdentityID` rejects there while
  the shared `fromTreeNodeID` stays permissive for the IDs that pre-date
  restore spans.
- Starting the watch pump before the first response made the loop's error
  the only thing keeping the document drained, and the reconnect site
  dropped it. A discarded error is load-bearing the moment the failing
  path owns a goroutine's lifetime: `retryWatchLoop` now retries until a
  loop comes up, the document is detached, or the watch context ends.
- A `connect` server-streaming client blocks in `Watch()` until the
  handler's first `Send` flushes headers, so "publish while the first
  response is outstanding" is not reachable from a test server. What is
  testable is the pump draining while the stream is idle, and the pump
  being gone after an initialization failure.

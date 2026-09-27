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
  counterpart for exactly that.
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
  nothing and costs reentrancy. It is back on `readLocked`.
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

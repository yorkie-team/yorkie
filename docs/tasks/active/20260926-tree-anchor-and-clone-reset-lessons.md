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

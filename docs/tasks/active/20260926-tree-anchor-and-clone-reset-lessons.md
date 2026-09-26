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

# Lessons: chained-merge convergence

**Created**: 2026-08-03

- The snapshot encoding constrains the runtime data model. Because only one
  `MergedFrom` pointer per child is persisted and `rebuildMergeState` reads
  the child's final physical parent, a merge chain can only ever be
  reconstructed in compressed form (P→R). Any runtime representation that is
  *richer* than what the snapshot can round-trip (a multi-hop P→Q→R chain)
  will diverge after a reload. The fix was to make the runtime match the
  snapshot's expressive power, not the reverse.

- Deriving the runtime forwarding pointer with the *same rule* as the reload
  path (`mergedInto == child's new parent`) is what guarantees the two agree.
  Whenever a runtime cache is rebuilt from persisted state on load, compute it
  the identical way at write time.

- Preserving the original source (`MergedFrom` stamped once) is what keeps the
  §1.1 redirect boundary resolvable after flattening; overwriting it on each
  hop was the original bug's second half.

- TDD paid off: the failing test encoded both convergence and snapshot
  round-trip up front, so the "flatten at runtime but reload disagrees" trap
  the issue warned about would have failed the round-trip assertion
  immediately rather than slipping through.

# Lessons: tree style merge-anchor boundary

**Created**: 2026-08-03

- The §1.1 insertion-boundary redirect is correct for inserts (RGA
  tie-break needs the position before the first moved child) but wrong
  for range boundaries: one position semantics cannot serve both, so
  the resolver takes an explicit boundary mode.
- The "right after the merge-source tombstone" position fixes both
  range directions at once: in visible coordinates it lands before the
  concurrent inserts and the moved children, so a range end excludes
  them and a range start includes them, matching the styling replica.
- Property-based testing surfaced this within one run of the pinned
  seed after Fix 19/20 landed — the shrunk counterexample was three
  operations. Convergence work benefits from keeping the PBT running
  after every fix; the next shrink immediately exposed the non-left-most
  variant (range end anchored after a moved child), now the follow-up.

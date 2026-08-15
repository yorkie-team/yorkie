# Lessons: tree style moved-anchor filter

**Created**: 2026-08-08

- The version-vector oracle has a blind spot for the styler's own
  concurrent inserts: causally known, positionally excluded. Filtering
  needs positional intent, not causality — hence the intended-parent
  stamp at insert time.
- `MergedFrom` turned out to be the right vocabulary for "logically
  belongs to that parent's sequence": stamping redirected inserts with
  it made the interloper filter a one-liner and aligned merge-delete
  propagation semantics for free.
- Guard-correctness tests (cases the filter must NOT touch) were as
  important as the divergence repro: the own-insert-inside-range case
  converges on main and would have silently broken under a stamp-less
  filter.
- Triage every remaining PBT counterexample against main before
  concluding: both survivors (from-side variant, edit-only 3-client)
  pre-exist, which separates "my regression" from "next finding" with
  one stash-run each.
- The filter's first exemption key (`MergedFrom == declaredParent`)
  was wrong for chained merges: Fix 20 keeps the ORIGINAL source on
  re-moved children, and mergedInto path compression destroys the
  "was it inside the declared parent" fact. A skip filter must fail
  open on anything it cannot positively classify — never diverge
  where main converged.
- Marshal is a weak oracle for attribute state: it hides both empty
  RHTs and removed-attr tombstones. The RemoveStyle guard was
  entirely unpinned (mutation run: disabling it stayed green) until
  the test asserted internal `Attrs` entries directly. Remote op
  deserialization allocates an empty RHT, so assert on entries, not
  container nilness.

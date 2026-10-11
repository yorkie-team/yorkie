# Split Boundary Anchors — Lessons

- A scope check is a placement rule too. `hasElementChild` decided which
  rules a split used, and it counted element tombstones, which each replica
  purges on its own schedule. Every input to a decision, including "does
  this rule apply", must read the same on every replica: tickets and the
  change's version vector, not local tombstones or GC timing.
- GC visits its pairs in map order. Any GC-time bookkeeping that depends on
  the order nodes are purged in, such as relinking anchors, has to give the
  same result in every order. Test it by repeating one pass many times.
- An index derived from node fields has to be cleared wherever those fields
  are. Clearing the records on a built tree without its index let a later
  purge bring a record back.
- A fuzz test is only a regression net if it fails on the code it guards
  against. Run it on main once: this one fails 332 of 600 runs there.
- Per-seed equivalence with the measured prototype was the safety net for
  every refactor in this task (15000 seeds identical after each step).
- Undoing a split is a merge. The plan's fuzz had inserts, splits and
  deletes only, so the undo regressions surfaced at the final review. Any
  tree change needs undo, redo and merges in its regression fuzz from the
  start.
- Measure fixes one at a time. Three changes measured together looked
  worse; measured apart, two helped and one hurt.
- A literal script replayed on a variant that behaves differently stops
  being the same scenario: later indices point elsewhere. Compare variants
  by seed, each generating its own script, and replay literally only to
  minimize against a fixed pair.
- Run the same measurement twice. A one-run difference between two
  versions was run-to-run nondeterminism from GC's map-ordered purges.
- Hold seeds out. Rules tuned on seeds 1-600 were checked on 601-1800
  before trusting the numbers.
- A fix that changes what users see, such as where a merge puts content,
  is a decision to raise before committing, even when it improves the
  numbers.
- When local rules keep accumulating to patch one interaction (here undo
  as merge), stop and ask whether the model needs that interaction built
  in (yorkie-js-sdk#1474).

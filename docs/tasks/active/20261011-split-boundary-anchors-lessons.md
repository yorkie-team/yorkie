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
- A pinned test has to fail with the fix turned off. Both new tests first
  passed without their fix: one divergence was only in tombstone order,
  which the replay's post-GC live comparison cannot see, and the other was
  guarded by a different line than assumed. Keep the inserted text live
  so the divergence shows in XML, and toggle the exact line under test.
- A fuzz regression against main is not necessarily new. Seed 322's
  minimized script diverges on main and HEAD alike; main's full run just
  did not reach it. Replay the minimized script on every baseline before
  calling it a regression.
- Every input to a placement must be readable through the change's version
  vector. A single LWW field (MergedAt, removedAt) holds whichever of
  several concurrent tickets a replica's order left there, so testing it
  against a version vector depends on arrival order; keep the set.

## Review round: cycle guards, peer-controlled lists, pinned regressions

- `preMergePoint` walked `InsPrevID` with no `nodeSet`, the one chain
  walk in the change that did not. The pointer reaches the tree from
  client bytes, so a chain looping back on itself spun the applying
  goroutine under the document lock; the test for it hangs for 10s
  without the guard and passes with it.
- A list a snapshot carries is a list a peer can send. `RestoreMerges`
  fed `addMergeTicket` one ticket at a time, and that call copies the
  whole set per insertion: linear-looking code, quadratic on an entry
  whose length the sender picks. Added `addMergeTickets`, which sorts
  the input once and merges the two runs.
- A known divergence recorded as a skipped test records nothing CI can
  read. `TestTreeBoundaryKnownDivergences` now runs in the unit lane and
  pins the tree each replica ends with, so any movement in those cases
  has to be re-recorded by hand; the env var still replays them as plain
  convergence checks.

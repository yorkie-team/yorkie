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

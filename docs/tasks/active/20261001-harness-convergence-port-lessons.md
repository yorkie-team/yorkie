# Lessons — port the agent-loop convergence work

**Created**: 2026-10-01

- A clean `git apply` is not proof that a hunk landed in the right place in a
  hand-diverged workflow. Compare the divergence sets instead: the lines that
  differ between the two repositories before the port and after it. Any
  difference between those sets is either an intended adaptation or a misplaced
  hunk, and here it was only the intended one.
- A rule that names a language feature does not port by search and replace.
  `it.fails` has no Go equivalent, and the nearest one (`t.Skip`) is exactly
  what the detector counts as a disablement. Decide which of the two stays
  true, then make the prompt, the detector and the wiring test agree. Here the
  detector stays strict and the prompt asks for the skip to be paired with a
  `--skipped` claim.
- In Go a disablement is a new line, not a renamed call. `it('x')` →
  `it.skip('x')` removes a case line; `t.Skip()` adds a line to an unchanged
  function. Counting it as a removed case would net it against any test added
  beside it, so it is counted apart, as upstream does for disabled suites.
- Run a new detector over real history before trusting its fixtures. 400 yorkie
  commits showed the build-constraint rule firing on a real narrowing
  (`bench` → `linux && bench`) and on a file moved out of the default lane,
  both of which are worth reporting, and no crashes on renames or deletions.
- Keep shared modules byte-identical, even their comments' issue numbers. The
  next sync is a `cmp`, and every local edit to a shared file makes it a merge.
- Look for a repository's test bodies outside its test-file naming before
  porting a detector keyed on that naming. yorkie keeps whole suites in
  `testcases/testcases.go`, which no `_test.go` rule sees.
- A clamp applied per unit and then summed is not the clamp of the sum. A
  signed tally summed across a round and clamped once is the only order that
  lets "added, then removed" net out.
- An inherited `|| echo ''` turns "could not read" into "read nothing", and a
  step outcome downstream then proves nothing. Gate on the value having been
  read, not on the step having succeeded.
- A byte-identical port inherits the source repo's blind spots, not the
  target's guards. `fingerprint.test.mjs` came from js-sdk without
  `fixtureGitEnv`, although this repo already had the helper. Before porting a
  test that shells out to git, check it against the target's own git-env rules.

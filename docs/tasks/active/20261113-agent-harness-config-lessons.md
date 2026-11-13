# Move the harness's repository-specific values into configuration — Lessons

**Created**: 2026-11-13

## Notes

- A guard over checkout STEPS is not a guard over what runs. The first
  version of `config-wiring.test.mjs` checked that every checkout and copy of
  `scripts/agent` also fetched the configuration, and counted the sites. Six
  changes that hand a pull request its own gates passed it: a new job running
  a script from a branch checkout, `repository:` on a `ref: main` checkout,
  `gh pr checkout` before the staging copy, a sparse pattern spelt
  `/scripts/agent`, an invocation path moved from `.trusted/` to the
  workspace, and the staged configuration overwritten after the export. The
  property is about execution, so the guard now walks each job, tracks which
  trees are trusted at each step, and checks every invocation.
- Keep the mutations as test cases. They run against the real workflows with
  one edit applied in memory, so they cannot drift from the files they guard.
- Two glob compilers lived in the package and disagreed on `?`, and both
  needed a directory before `**/x`. On a refusal surface a pattern in another
  tool's syntax compiles, matches nothing and takes its entry off the list.
  Refuse the syntax instead of reading it literally.
- `**/` as zero-or-more directories changed no tracked path's result. It does
  change paths this repository does not have: a file directly under `docs/`
  is now prose, and root-level `fixtures/` and `testdata/` are code-adjacent.
- The "is this module the entry point" check compared `process.argv[1]` with
  `import.meta.url` as strings. Through a symlinked path they differ, the CLI
  body is skipped, and the process prints nothing and exits 0. `config.mjs`
  realpaths both sides; the other CLIs in the package still use the string
  comparison.
- Two directories up from a staged copy is not a repository root. The default
  location applies only when the package sits at `<root>/scripts/agent`.
- `$GITHUB_ENV` reaches later steps of the job, not the step that writes it,
  and the fixer's shell need not inherit it. The pre-push hook exports the
  configuration directory itself, and the commands the fixer is told to run
  carry the variable on the command.
- A snapshot taken before the move was the cheapest proof that moving values
  changed nothing. Compare whole exports and results over `git ls-files`
  between the base and the head, not a sample.

## Merging

Runs that started before the merge keep the old workflow file but check out
`main`'s scripts, which then need a configuration the old file does not
fetch. Pause the pipeline, let running jobs drain, merge, resume.

## Self review

Two independent review rounds; findings and their fixes are in the pull
request description.

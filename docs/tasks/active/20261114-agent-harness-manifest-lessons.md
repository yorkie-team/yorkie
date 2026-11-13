# Detect drift in copies of the shared agent harness — Lessons

**Created**: 2026-11-14

## Notes

- A manifest checked only against its own tree proves nothing about a copy:
  whoever edits a file can regenerate the list. It takes a second record, kept
  outside the shared files, of which manifest was adopted — and a third check
  that the record is true of the repository it names. Each one catches what
  the one before lets through.
- "Tracked" cannot be asked of git where the check has to run. The package's
  tests run in copies of a tree with no `.git`, so the shared set is a rule
  (the package, minus the manifest, `node_modules` and anything dot-named) and
  a test compares the rule with `git ls-files` where git is there.
- A network check that passes when it could not reach the network is not a
  check. The comparison that matters fails closed; the one that only informs
  (is upstream ahead?) warns, including when it cannot be made.
- The sync writes only paths that pass the same predicate the manifest is
  built from. "Never touches the repository's own half" is then a property of
  the path filter, not of the copying code being careful.
- Run the new loader against the old configuration as a child process. The
  schema that matters is the one just copied, not the one already imported.
- `git status --porcelain` lines start with a significant space. Trimming the
  output shifts the first path by one column and the status check passes on a
  dirty tree.
- A step that installs what a checked-out commit asks for belongs after the
  step that checks it is the commit that was reviewed.

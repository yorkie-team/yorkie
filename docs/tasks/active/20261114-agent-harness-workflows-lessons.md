# Make the agent workflows read the repository's values from configuration — Lessons

**Created**: 2026-11-14

## Notes

- A local composite action cannot be removed after it is used. The runner
  re-reads `action.yml` from disk before it runs the action's post steps and
  fails the job when the file is gone (`ActionManager.LoadAction`; the post
  steps themselves come from a cache filled when the action first ran). So
  the trusted copy of the toolchain action stays in the workspace until the
  job ends, listed in `.git/info/exclude`, where every other trusted
  side-directory is moved out or deleted before the agent starts.
- `uses: ./…` is execution with the job's tokens, read from the workspace at
  the step that runs it. In a job that has checked out a pull request's
  branch that is the branch's file. The guard now treats it like a script.
- An expression naming an output nobody wrote is an empty string, and nothing
  reports it. Step outputs are asked for by name, the reader refuses a name
  it does not know, and a test reads the workflows for the other half: a
  misspelt reference, a reader that a skipped `if:` would leave empty.
- A value that may legitimately be empty should not look like one that failed
  to arrive. The diff pathspecs always start with `.`, so the diff step can
  refuse an empty value instead of quietly reviewing an unfiltered diff.
- A step that needs a configured value before its job has a trusted tree
  takes it from an earlier job's outputs. The review job computes its diff
  with a `git fetch` in the workspace, and the guard rightly stops trusting a
  side directory in a workspace after that.
- One configured sentence cannot keep two prompts' line wrapping. The proof
  that the prompts did not change compares them with whitespace collapsed.
- Where the two repositories' sentences differ in structure, the configured
  value is the whole sentence (the issue-to-PR "verify" step). Where they
  differ in a noun phrase, it is the phrase.
- `workflow_dispatch` runs from the ref the dispatcher picked. A checkout
  with no `ref:` there is the branch under test, not the default branch.

## Merging

Runs that started before the merge keep the old workflow file but check out
`main`'s scripts and configuration. The old prompts carry their own text, so
they are unaffected; the old files do not call `workflow-values.mjs` at all.
The configuration's `fixer` keys change shape in the same merge, which only
the new files read. Pause the pipeline, let running jobs drain, merge, resume.

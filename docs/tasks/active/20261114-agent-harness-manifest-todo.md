# Detect drift in copies of the shared agent harness

**Created**: 2026-11-14

## Problem

The scripts under `scripts/agent/` and the `agent-*.yml` workflows are now the
same bytes in two repositories. Nothing keeps them that way. A fix made by
hand in the copy looks like any other change there, survives until the next
sync overwrites it, and which upstream commit a copy corresponds to is
written in prose in a README.

## Goal

A hand edit to the shared files fails CI in the repository that made it, and
"which commit is this copy" is a file a program reads.

## Plan

- [x] `manifest.mjs`: `build` writes `scripts/agent/MANIFEST.sha256` over the
      shared files, `verify` names what was added, removed or changed,
      `digest` prints one hash for the manifest
- [x] Which files are shared is decided by rule, without git, so the check
      also runs in a tree that has no `.git`
- [x] `.github/agent-harness/upstream.json`: `repository: null` where the
      files are written; repository, commit and manifest digest in a copy
- [x] Tests in the package: the tree matches the manifest; a copy's manifest
      digest is the one it recorded
- [x] `agent-scripts.yml`: compare the record with the named repository at the
      named commit, failing when it cannot be fetched; warn when upstream's
      default branch has moved on
- [x] `sync.mjs --from <checkout>`: replace the shared files, record the
      source, report configuration keys the new schema adds or drops
- [x] README: adopting and syncing, the per-repository files, the three checks
- [x] `agent-fix.yml`: set the toolchain up after the checked-out commit is
      re-verified, not before; the trust guard holds the order
- [x] `npm test`, `npm run test:repo`, actionlint, `make verify`; the package
      suite on a copy of the other repository's tree, passing, then failing
      with the path named after one byte is changed
- [x] Review round: the recorded commit must be on upstream's default branch
      (asked of the repository, with retries, failing closed); `sync.mjs`
      refuses a source off that branch, validates everything before its first
      write, never deletes an untracked or ignored file, and never writes
      through a symbolic link; line endings pinned by `.gitattributes`
- [ ] A run of the upstream comparison on a runner, in a repository that is a
      copy
- [ ] Review rounds on the pull request

## Follow-ups (recorded, not done)

- `agent-iterate-ci.yml` checks out the branch tip while acting on
  `workflow_run.head_sha`, with no re-verification, and a push during the run
  does not cancel it unless the new CI run also fails. Recommended: a neutral
  skip when `git rev-parse HEAD` is not `head_sha`, before the toolchain, and
  the same in the panel's fix job — after which the trust guard can require
  toolchain-after-re-verification in all three jobs that act on a reviewed
  commit.
- The weekly "upstream has moved on" signal is a warning on a green run. An
  adopting repository that wants to be told needs its own reminder. (Taken
  up in `20261114-agent-harness-notify-todo.md`.)

## Not in this change

- `sync.mjs` reads a local checkout only. Fetching one is `git clone`.
- A private upstream: the comparison uses the job's read-only token, which
  reads public repositories.
- The three jobs that check out a pull request's branch tip without
  re-verifying it (the panel's fix job, the CI-fix arm, the review reply) are
  unchanged. Whether the first two should re-verify is an open question,
  recorded in the pull request.

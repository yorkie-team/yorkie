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
- [ ] A run of the upstream comparison on a runner, in a repository that is a
      copy
- [ ] Review rounds on the pull request

## Not in this change

- `sync.mjs` reads a local checkout only. Fetching one is `git clone`.
- A private upstream: the comparison uses the job's read-only token, which
  reads public repositories.
- The three jobs that check out a pull request's branch tip without
  re-verifying it (the panel's fix job, the CI-fix arm, the review reply) are
  unchanged. Whether the first two should re-verify is an open question,
  recorded in the pull request.

**Created**: 2026-10-09

# Remove the trusted-tree guard and the git-hook snapshot

Design: [local-enforcement-layer.md](../../design/local-enforcement-layer.md)

## Problem

`.githooks/trusted-tree.sh` refused `pre-commit` and `pre-push` whenever the
checkout carried commits this clone did not create. Every agent-loop branch
carries bot commits, so a maintainer needed `YORKIE_ALLOW_FOREIGN_TREE=1` on
every commit and push there. The `$GIT_DIR/githooks` snapshot existed for the
same threat model and went stale until `scripts/setup.sh` was re-run. The
maintainer chose wafflebase's model: `core.hooksPath = .githooks`.

## Plan

- [x] Delete `.githooks/trusted-tree.sh`; drop its sourcing and
      `yorkie_require_own_work` from `pre-commit` and `pre-push`, keeping
      `make lint` / `make verify`; rewrite the hook headers
- [x] `scripts/setup.sh`: `git config core.hooksPath .githooks` instead of
      the snapshot; scope the re-run guard to the Claude Code hook sources
      it still persists, and drop the untracked-file check that only
      `cp .githooks/*` needed
- [x] Decide on `scripts/hooks/install.mjs`: keep its `$GIT_DIR` snapshot
      (fires on session open, not on a chosen commit or push)
- [x] `scripts/test/harness-hooks.test.mjs`: drop the trust-guard and
      git-snapshot cases; add setup wiring, worktree, and "gates run on a
      branch with somebody else's commits" cases
- [x] Docs: design doc (decision + alternatives), CONTRIBUTING.md (with the
      one-time migration step), scripts/README.md
- [x] Verify

## Review

- `node --test scripts/test/*.test.mjs`: 163 pass, 0 fail.
- `GOTOOLCHAIN=go1.26.0 make lint`: 0 issues.
- `verify-doc-links.mjs` and `verify-doc-index.mjs` pass.
- Throwaway clones, `make`/`golangci-lint` stubbed, checkout of a branch with
  a bot-authored commit, then `git commit` of a staged `.go` file:
  - `origin/main`: setup snapshots into `.git/githooks`; the commit is
    refused ("not created by this clone").
  - this branch: `core.hooksPath=.githooks`; the commit runs `make lint`
    and lands; `git push` runs `make verify` and succeeds.

Existing clones keep `core.hooksPath` at the old `$GIT_DIR/githooks` copy
until `bash scripts/setup.sh` is run once more.

## Review follow-ups (#2166)

- [x] `scripts/setup.sh --check`, run from `make lint`: warns, never fails,
      on an unset or legacy `core.hooksPath`; silent under `CI`
- [x] `setup.sh` removes `$GIT_COMMON/githooks` only when `core.hooksPath`
      resolves to it, after the source check; a refused run changes nothing
- [x] Test through git's own hook dispatch: setup, bot commit, `git commit`
      with a stubbed `make`, marker file and landed commit asserted
- [x] Tests for the `--check` text and the conditional removal
- [x] Design doc: hook scripts are branch-controlled again and why that is
      accepted; the rejected "trust `origin/*`, guard fork commits"
      alternative; a checkout without `.githooks/` runs no hooks

**Created**: 2026-09-26

# Port the local-harness hook fixes back from yorkie-js-sdk

yorkie-js-sdk ported `.githooks/trusted-tree.sh`, `scripts/setup.sh` and
`scripts/hooks/install.mjs` from this repository (yorkie-js-sdk #1384), and
its three self-review rounds plus CodeRabbit found defects that came over
verbatim from here. Each was fixed there with a test that failed first. This
task brings the fixes home, keeping this repository's Go shape (`make lint`,
`make verify`, the `*.go` filter, `scripts/agent/git-env.mjs` in the tests).

## Defects

1. `setup.sh` and `install.mjs` snapshot into `--absolute-git-dir`, which in a
   linked worktree is `.git/worktrees/<name>`. `core.hooksPath` is shared
   config, so removing that worktree silently disables every hook in the
   clone.
2. The reflog parser refuses your own work after `git pull --rebase`
   (`pull --rebase … (pick): …`) and after a pull merge
   (`pull …: Merge made by …`).
3. `rebase (finish)` counts as "created". After a rebase that only
   fast-forwarded onto a fetched pull request it names someone else's commit.
4. Only HEAD's reflog is read, and it is per worktree: a branch committed in
   one worktree is refused in another.
5. Only `origin/main` is trusted. A fork contributor rebasing onto
   `upstream/main` while the fork's `main` lags is refused on every commit.
6. `cherry-pick --ff` logs a lowercase `cherry-pick: fast-forward` against
   the foreign OID; the case-sensitive `Fast-forward` skip misses it.
7. The author check uses `%aE`, which applies the branch's own `.mailmap`.
8. The parser matches on the first word only. In yorkie that ACCEPTS
   `commit (amend)` already (js-sdk's `$2`-only form did not); the port
   replaces it with a whole-subject match, so pin the amend and merge forms
   with tests either way.

Also: `setup.sh` ignores untracked hook sources in its comparison while
`cp .githooks/*` copies them; its comment should say the refusal guards
against accident only; scratch repos in the tests need
`commit.gpgsign=false`.

## Plan

- [ ] Pin `commit.gpgsign=false` in the test scratch repos
- [ ] Snapshot into the common git dir (setup.sh, install.mjs) — worktree test
- [ ] Reflog parser: accept `pull --rebase` picks, pull merges, amend/merge
      commits
- [ ] Reflog parser: refuse `rebase (finish)` and lowercase `fast-forward`
- [ ] Read the current branch's reflog too — cross-worktree test
- [ ] Trust `upstream/main` in the guard and prefer it in setup.sh — fork test
- [ ] Raw `%ae` for the author check — `.mailmap` test
- [ ] setup.sh: count untracked hook sources; say the refusal is accident-only
- [ ] Update `docs/design/local-enforcement-layer.md` and CONTRIBUTING.md
- [ ] `node --test scripts/test/*.test.mjs` green; Red shown for each new test

## Not ported

- `setup.sh --check`: js-sdk runs it from pnpm's `prepare` to surface a clone
  with no hooks. yorkie has no package-manager install step to call it from,
  so it would be dead code here.
- pnpm, lint-staged, the `examples/` filter, `verify-license` `.ts` changes.

## Review

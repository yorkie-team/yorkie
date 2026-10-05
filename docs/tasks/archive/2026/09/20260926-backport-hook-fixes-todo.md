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

- [x] Pin `commit.gpgsign=false` in the test scratch repos
- [x] Snapshot into the common git dir (setup.sh, install.mjs) — worktree test
- [x] Reflog parser: accept `pull --rebase` picks, pull merges, amend/merge
      commits
- [x] Reflog parser: refuse `rebase (finish)` and lowercase `fast-forward`
- [x] Read the current branch's reflog too — cross-worktree test
- [x] Trust `upstream/main` in the guard and prefer it in setup.sh — fork test
- [x] Raw `%ae` for the author check — `.mailmap` test
- [x] setup.sh: count untracked hook sources; say the refusal is accident-only
- [x] Update `docs/design/local-enforcement-layer.md` and CONTRIBUTING.md
- [x] `node --test scripts/test/*.test.mjs` green; Red shown for each new test

## Not ported

- `setup.sh --check`: js-sdk runs it from pnpm's `prepare` to surface a clone
  with no hooks. yorkie has no package-manager install step to call it from,
  so it would be dead code here.
- pnpm, lint-staged, the `examples/` filter, `verify-license` `.ts` changes.

## Review

Every fix landed with a scratch-clone test that runs the real hook (with
`make` and `golangci-lint` stubbed on `PATH`) or the real `setup.sh`. Red
before each fix:

| Fix | Test | Red |
|-----|------|-----|
| Common git dir | setup.sh in a worktree installs into the shared git dir | hooksPath was `.git/worktrees/wt/githooks`; with setup.sh fixed, install.mjs's settings still named `worktrees` |
| `pull --rebase` picks | the trust guard accepts your own commits after git pull --rebase | refused, "not created by this clone" |
| pull merge | the trust guard accepts your own merge made by git pull | refused, "not created by this clone" |
| `rebase (finish)` | the trust guard refuses a rebase that only fast-forwards onto a PR | `RAN make verify` |
| `cherry-pick --ff` | the trust guard refuses commits reached by cherry-pick --ff | `RAN make verify` |
| Branch reflog | the trust guard accepts a branch committed in another worktree | refused, "not created by this clone" |
| `upstream/main` | the trust guard accepts a fork branch rebased onto upstream/main; setup.sh compares against upstream/main in a fork | refused; setup "differ from origin/main" |
| Raw `%ae` | the author check ignores the branch's own .mailmap | `RAN make verify` |
| Untracked sources | setup.sh refuses an untracked hook it would install | installed |
| gpgsign | (existing suite under a HOME with `commit.gpgsign=true`) | 6 tests failed |

`commit (amend)` and `commit (merge)` were already accepted here: yorkie's
first-word match tested `$2 ~ /^(commit|…)/`, which the qualifier in `$3`
does not disturb; the defect was in an intermediate js-sdk form. The two
tests pin them against the whole-subject rewrite, and were green before
and after.

The `(finish)` and `cherry-pick --ff` fixtures author the PR commit as the
local identity. With a different address the author check refuses anyway
and the misclassification is invisible; with yours, `make verify` ran.

Verification: `node --test scripts/test/*.test.mjs` 167/167; `make verify`
green; `scripts/agent` untouched. shellcheck not installed, not run.

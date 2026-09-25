**Created**: 2026-09-26

# Lessons — porting the hook fixes back from yorkie-js-sdk

- A port's defect list is not the source's. yorkie's first-word match
  already accepted `commit (amend)`; per js-sdk's lessons an intermediate
  form of its copy refused it (the squash hides which). Write the Red test
  here before assuming a fix applies.
- A trust-guard test with a stranger's address cannot see a reflog
  misclassification: the author check refuses underneath and the test stays
  green for the wrong reason. The first `(finish)` and `cherry-pick --ff`
  Reds failed only on the refusal text. Author the foreign commit as the
  local identity — the spoof the reflog exists for — and the Red becomes
  `make verify` actually running.
- `fixtureGitEnv` pins GIT_DIR, so it cannot drive a linked worktree.
  `repoScopedEnv(root)` strips the same variables and caps discovery at the
  scratch root, which is what the worktree cases need.
- `tasks-index.sh` regenerates rows for other tasks' drift too. Keep only
  this task's row so the commit carries one intent.

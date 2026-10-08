# Lessons: remove the trusted-tree guard

- A guard that refuses the everyday case gets bypassed by reflex. The trust
  guard was written for a reviewer committing in a stranger's checkout, but
  its daily traffic was maintainers finishing agent-loop branches, where bot
  commits are the norm. Check who actually trips a guard before adding rules
  to it.
- Separate the two hook systems by trigger, not by mechanism. Git hooks fire
  on a commit or push someone chose; Claude Code hooks fire when a session
  opens. The same "branch-supplied code" argument lands differently on each,
  so only the git half moved to live wiring.
- When removing what a re-run guard protected, re-scope the guard instead of
  keeping its old list. `.githooks` left the compared set, and the
  untracked-file check went with the `cp .githooks/*` it existed for.
- A relative `core.hooksPath` is resolved per worktree, so one shared config
  value serves every linked worktree; `git rev-parse --git-path hooks` shows
  where git will look.

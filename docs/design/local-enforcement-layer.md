---
title: local-enforcement-layer
target-version: 0.7.24
---

# Local Enforcement Layer

## Problem

`CLAUDE.md` step 2 requires every commit to be lint-green and tested. Until
this layer existed, nothing checked that before CI — the repository gated a
change exactly once, at the end, and the first thing a contributor heard about
a lint violation was a red lane on a commit that had already been pushed. The
only git hook was `commit-msg`, which checks the shape of a message and
nothing about the tree.

The cost was not hypothetical. `docs/design/agent-command-verbs.md` §4b listed
the Apache licence header under "enforced by nothing", and it had drifted: 17
of 486 tracked `.go` files carried no header, accumulated over five years.
That is what a convention with no lane behind it looks like.

Adding local gates is not free, and the part that needs designing is not the
gates. **A hook is code that runs on a contributor's machine without being
asked for.** The obvious implementation — track the hooks, point
`core.hooksPath` at the tracked directory, commit a `.claude/settings.json` —
hands every pull request arbitrary execution on the machine of anybody who
checks it out. Reviewing a patch is supposed to be reading it, not running it.
So this document is mostly about where the hook code lives and whose tree it
is allowed to run.

### Goals

- Gate a change locally before CI, layered so the expensive check runs least
  often.
- Make the code of a gate something a branch cannot supply.
- Give the hooks, the documentation and the agent pipeline one aggregate
  verification target to name, instead of a prose list each copy of which
  rots separately.

### Non-Goals

- The integration lane. `make test` needs MongoDB, which a commit or a push
  cannot assume is up; CI runs it against a real container.
- A Go equivalent of the upstream `verify-self` lane runner and its
  `.harness-reports/` artifact. It is the largest remaining gap — it is what
  would feed `agent-iterate-ci`'s diagnosis, which today reads
  `gh run view --log-failed | tail -c 40000` — and it needs its own design.
- A local runner for the six-lens review panel. `.claude/commands/self-review.md`
  records that its absence is deliberate.
- Protecting a machine whose owner *wants* to run a branch. Every refusal here
  has a one-line bypass, stated in the refusal.

## Design

### Four layers, ordered by cost

| Layer | Runs | Cost | Gate |
|---|---|---|---|
| Claude Code hooks | every edit / session start | ~0 | `guard-generated-files.sh`, `session-prime.sh` |
| `.githooks/pre-commit` | every commit | ~6 s | `make lint` |
| `.githooks/pre-push` | every push | ~40 s | `make verify` |
| CI | every push | minutes | everything, including integration |

The split between the two git hooks came from measurement rather than taste:
on the machine this was built on, `make lint` is 5.5 s warm and
`go test ./...` is 35 s cold (1705 tests, 81 packages). Six seconds per commit
is a gate people keep; forty is a gate people disable, and a disabled gate
enforces nothing. The tests are therefore one layer out, where the cost is
paid once per push instead of once per commit.

`make verify` is the aggregate target the whole layer names:

```make
verify: lint verify-license
	go test ./...
```

Defining it once means the hook, `CLAUDE.md`'s command table and a future
agent all call the same thing. `make test` is deliberately not one of its
prerequisites.

### The load-bearing decision: both hook systems install from a `$GIT_DIR` snapshot

`scripts/setup.sh`, run once per clone, does not point anything at the working
tree:

1. It copies `.githooks/*` into `$GIT_DIR/githooks` and sets
   `core.hooksPath` to **that copy**. `$GIT_DIR` here is the clone's common
   git dir (`git rev-parse --git-common-dir`), never a linked worktree's
   `.git/worktrees/<name>`: `core.hooksPath` is shared by every worktree, so
   a snapshot inside one died with it and left the whole clone running no
   hooks, silently.
2. It runs `scripts/hooks/install.mjs`, which copies `scripts/hooks/*.sh` into
   `$GIT_DIR/agent-hooks/` and writes wiring that names **those copies** into
   `.claude/settings.local.json`, which is gitignored.

**Why.** A tracked hook is code the branch supplies. With
`core.hooksPath = .githooks`, a pull request can rewrite `pre-commit`, and a
reviewer who runs `gh pr checkout` and commits anything at all executes
whatever it now says. The Claude Code half is worse, because the *wiring* is
branch-supplied too: Claude Code reads project settings out of the working
tree and runs the commands they name, with no confirmation, at `SessionStart`
and before every `Edit`/`Write`. A tracked `.claude/settings.json` therefore
means opening a session in a checkout of somebody's branch runs that branch's
`scripts/hooks/*.sh`. Both halves are ordinary tracked files any contributor
can rewrite, and the generated-file guard protects neither.

`git checkout` materialises tracked paths into the WORKTREE; it does not
write the snapshot directories. (It does write inside `$GIT_DIR` — `HEAD`, the
index, the reflog — which is why the claim has to be about those directories
and not about `$GIT_DIR` as a whole.) So the hook that runs is the one present
when a human ran setup, whatever branch the worktree is on, and a checkout of
an untrusted branch wires nothing at all — the state the repository was in
before the hooks existed.

**The cost is staleness.** An improved hook reaches a clone only when somebody
re-runs `scripts/setup.sh`. That is the right direction to fail: a stale guard
still refuses the edits it knew about, and CI is the backstop that closes the
set. The alternative — live code from the tree — is the property being
removed, not a feature being lost.

### What the snapshot does not close

The snapshot pins **which** script runs. It says nothing about **what** that
script invokes. `pre-commit` execs `make lint`, which resolves through the
working tree's `Makefile` and its `.golangci.yml` — whose `linters.custom` can
name any loadable plugin. `pre-push` execs `make verify`, which additionally
runs `go test ./...`, compiling and executing every `_test.go` in the tree
including the branch's own `TestMain`. A gate that checked anything other than
the working tree would not be a gate.

There is no file list that can pin this. "Refuse when `Makefile` or
`.golangci.yml` differ from upstream" closes two paths and leaves the widest
one open, because running the tree is the whole job. What separates *my work*
from *a pull request I am reviewing* is not a set of paths but the provenance
of the commits this checkout carries on top of the upstream default branch. So
that is what `.githooks/trusted-tree.sh` checks, sourced by both hooks before
either reaches `make`.

**Provenance comes from the reflog, not from the author line.** An earlier
revision compared the author address against the local `user.email`, which is
not an authentication decision at all: the author address is a field the
branch's own author writes, so `git config user.email maintainer@example.com`
before committing walks straight through. Every address in this repository's
history is public, so the spoof needs no secret. The address that is still
compared, underneath, is the raw `%ae`: the mailmapped `%aE` applies
`.mailmap`, a tracked file the branch supplies, and would let it map its
author onto yours.

The reflog is the credential. It lives in `$GIT_DIR`, only the local git writes
it, and no content a fetched branch carries can add an entry to it. Two are
read: HEAD's, which is per worktree, and the current branch's, which every
worktree shares — so a branch written in one worktree stays yours when it is
checked out in another. A commit this clone **created** has an entry that
records git writing it, matched on the whole subject: `commit` with or
without a qualifier (`(amend)`, `(merge)`), `cherry-pick`, `revert`, `am`, a
rebase step that writes a commit (`(pick)`, `(reword)`, `(edit)`, `(squash)`,
`(fixup)`, `(continue)`, spelled `rebase` or `pull --rebase`), or a merge git
made (`merge …: Merge made by`, `pull …: Merge made by`). A commit this clone
merely **received** is known only through `clone:`, `fetch`, `checkout:`,
`reset:`, a fast-forward in any case (`cherry-pick --ff` logs a lowercase one
against the foreign OID), or a rebase's `(start)` / `(finish)` — `(finish)`
names the commit HEAD lands on, which after a rebase that only fast-forwarded
onto a fetched branch is somebody else's. An unrecognised subject counts as
not-creating, so a future git spelling fails closed.

Commits reachable from `origin/main` or `upstream/main` are trusted by
construction, so rebasing onto a fetched `main` does not trip it. Both,
because contributors work from forks: `origin/main` is then the fork's and
usually lags, and every upstream commit it lacks would read as foreign. Only
those two names — `gh pr checkout` can add a remote named after the author's
fork, and that remote's `main` is not the default branch.

What it does not catch: a branch authored under **your** address that you
then rewrite yourself — rebase, amend, `am`, a `pull --rebase`. The rewrite
writes the commits here and the author matches. Read the diff before
rewriting someone else's branch.

The honest cost: a commit written on another machine and fetched into this
clone was not created here, and is refused. That is the same evidence a
stranger's commit presents. `git commit --no-verify`, `git push --no-verify`
and `YORKIE_ALLOW_FOREIGN_TREE=1` are the bypasses, and every refusal names
one.

### The re-run vector

`scripts/setup.sh` is itself the hole the snapshot does not cover, because the
documentation tells people to re-run it to pick up improved hooks. Run inside
a checkout of somebody's pull request, it makes that branch's `pre-commit`,
`pre-push` and `scripts/hooks/*.sh` the permanent, checkout-proof hooks of the
clone.

So it compares the hook sources against `upstream/main` (or, without one,
`origin/main`) first and refuses when they differ — untracked files included,
since `git diff` skips them and `cp .githooks/*` does not — with `YORKIE_ALLOW_LOCAL_HOOKS=1` as an explicit escape rather
than a prompt — the script also runs non-interactively, and a maintainer
editing the hooks means it where a reviewer almost never does. This guards
against accident only: a hostile branch's `setup.sh` can simply leave the
check out, and running it is already running the branch's code.

**What is compared is what the script runs, not what it is named after.** The
first version of the list covered `.githooks`, `scripts/hooks` and
`scripts/setup.sh`, while the script's last line executes
`scripts/hooks/install.mjs`, which imports `../direct-run.mjs`. A branch whose
only change under the trust surface was that one file passed the guard and ran
its code. The list is now `:(glob)scripts/*.mjs`, covering every sibling
module a future import could reach — `:(glob)` magic because git's default
`*` spans `/` and would otherwise pull in all of `scripts/agent/**`.

### Refuse rather than skip, where nothing reads the skip

Two gates in this layer depend on a tool that may be absent, and they answer
differently on purpose:

- `make verify-license` prints `SKIPPED` when Node is missing. A person who
  typed the command sees the line.
- `.githooks/pre-push` refuses outright when Node is missing. Git reads only
  the exit status, and a printed line would scroll past under 35 s of test
  output — a push would succeed with the licence gate silently absent.
- `.githooks/pre-commit` skips when the commit stages no Go at all (an outside
  contributor fixing a README must not need the Go toolchain to commit), and
  refuses when Go **is** staged and `golangci-lint` is missing. "Nothing to
  lint" and "no linter" must not share an answer.

`pre-commit` decides on `git diff --cached --name-only -- '*.go'` with no
`--diff-filter`: `ACM` drops `R`, and git reports a rename-with-edit as a
single `R` entry, so such a commit would stage Go and skip the lint entirely;
`ACMR` still drops `D`, and a deletion breaks compilation for everything that
referenced it. It lints the working tree rather than the index, which a
`git add -p` commit can exploit — linting an index-only checkout costs a
temporary worktree per commit, and `pre-push` and CI both see the real tree.

### The licence gate has two homes, and neither alone is enough

`scripts/verify-license.mjs` fails on any `.go` file without the Apache grant
clause in its first 40 lines. It matches the clause alone — the tree carries
two comment styles and copyright years from 2020 on, and a stricter checker
would spend its findings on formatting. Generated files are in scope, because
a `buf` plugin change that dropped the header is exactly the case worth
catching.

It runs from `make verify` (so the push gate holds it) **and** unconditionally
from `ci.yml`. Both, because `make verify` needs Node locally, and `ci.yml` is
the only workflow `agent-iterate-ci.yml` subscribes to: a gate that reds
anywhere else stops an agent-managed pull request with nothing watching it.

### Every guard here fails silently, so every guard here is pinned

`scripts/test/harness-hooks.test.mjs` reads *this* tree rather than a planted
one, which is unusual for this repository's suites and deliberate: the facts
are about this tree, and a planted copy of them would assert only that the
test agrees with itself. It pins that the generated-file guard refuses every
generated file actually present (the guard fails open by design, so a `case`
pattern that stops matching does not error — it stops guarding), that every
script the installer wires exists and is executable, that no
`.claude/settings*.json` is **tracked** (asked of git, because `.gitignore` is
not a security control), and that both git hooks consult the trust guard
before reaching `make`. The installer and the trust guard are exercised for
real in scratch clones, with `make` and `golangci-lint` stubbed on `PATH`:
`setup.sh` installing into the common git dir from a linked worktree and
refusing changed or untracked hook sources, and one case per reflog form the
guard accepts or refuses, per trusted base, and per worktree.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A branch rewrites a hook, and a reviewer's commit runs it | Hooks are snapshotted into `$GIT_DIR`, where a checkout materialises no tracked path |
| A branch supplies what the hook *invokes* (`Makefile`, `.golangci.yml`, `TestMain`) | `trusted-tree.sh` refuses a checkout carrying commits this clone did not create |
| The provenance check is spoofed by setting `user.email` | Decided from the reflog, which lives in `$GIT_DIR`; the author line (raw `%ae`, no `.mailmap`) is a second condition, never the only one |
| `setup.sh` re-run inside a pull-request checkout persists that branch's hooks | Hook sources, untracked ones included, compared against `upstream/main` or `origin/main`; explicit `YORKIE_ALLOW_LOCAL_HOOKS=1` to proceed |
| `setup.sh` run in a linked worktree, which is later removed | The snapshot lives in the common git dir that `core.hooksPath`, shared config, points every worktree at |
| A future import widens the trust surface past the compared list | The list is `:(glob)scripts/*.mjs`, not the importers |
| The snapshot goes stale | Documented re-run of `scripts/setup.sh`; CI is the backstop, and a stale guard still refuses what it knew |
| A cheap gate becomes expensive and gets bypassed by reflex | Layered by measurement: lint at commit, tests at push, integration in CI |
| A gate is silently absent because its tool is missing | Refuse rather than skip wherever only an exit status is read |
| A guard stops guarding without failing | `harness-hooks.test.mjs` pins each one against this tree |
| A contributor cannot run the gates at all | `scripts/setup.sh` is opt-in and Node is optional on the Claude-hook leg; CI holds every gate unconditionally |

### Design Decisions

| Decision | Reason |
|----------|--------|
| `make lint` at commit, `make verify` at push | 5.5 s vs 35 s measured; the per-commit gate has to stay cheap enough to keep |
| Snapshot into `$GIT_DIR` rather than `core.hooksPath = .githooks` | A tracked hook is branch-supplied code that runs on a reviewer's machine |
| `.claude/settings.local.json`, gitignored, never tracked | Claude Code runs what project settings name, unconfirmed, at session start and before every edit |
| Provenance from HEAD's and the branch's reflog | The author address is a field the branch writes; the reflog is written by the local git |
| Opt-in via `scripts/setup.sh` | Installing hooks on a clone without being asked is the behaviour being refused, one layer up |
| A Node licence checker rather than a Go tool | Node was already in the workflow, the `Docs` workflow was the right unfiltered home, and clause-matching is more forgiving than template comparison across two comment styles |
| `pre-commit` lints the working tree | An index-only checkout costs a temporary worktree per commit; `pre-push` and CI see the real tree |
| `session-prime.sh` exits silently under `GITHUB_ACTIONS` | Its content is a local multi-commit workflow; a CI fix job is told to fix what it was handed and nothing else |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Track `.claude/settings.json` so the hooks wire themselves | Checking out a branch would then execute that branch's hook scripts with no confirmation |
| `git config core.hooksPath .githooks` | Same failure for the git half: a pull request rewrites `pre-commit` and a reviewer's commit runs it |
| Refuse when `Makefile` / `.golangci.yml` differ from upstream | Closes two of three paths; `go test ./...` runs every `_test.go` in the tree, and there is no file list for that |
| Compare the commit author address against `user.email` | The branch's own author writes that field, and `.mailmap` can rewrite it afterwards |
| Run `go test ./...` in `pre-commit` | 35 s per commit; the gate would be bypassed, and a bypassed gate enforces nothing |
| Leave enforcement to CI alone | A lint failure then costs a full CI round, and on an agent-managed branch one of `agent-iterate-ci`'s bound of four fix rounds |
| `go install github.com/google/addlicense` + `addlicense -check ./...` | Close. One line, no second ecosystem, no skip path, and it can fix as well as check. Not taken because Node was already here and the `Docs` workflow was the right home; the honest cost is that `make verify` cannot run the licence check without Node. If the Node dependency becomes a burden, this is the swap |
| Port `require-ai-disclosure.sh` from upstream | It does nothing unless an environment variable is set, and nothing in this repository sets it — a hook present and permanently asleep |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents. The
layer itself landed under
[20260924-local-harness-enforcement-todo.md](../tasks/archive/2026/09/20260924-local-harness-enforcement-todo.md),
whose lessons file records the three review rounds behind the decisions above.
The reflog, worktree and fork fixes were found by yorkie-js-sdk's review of its
port and brought back under
[20260926-backport-hook-fixes-todo.md](../tasks/active/20260926-backport-hook-fixes-todo.md).

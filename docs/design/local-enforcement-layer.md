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
asked for.** The two hook systems answer that differently, because they fire
on different acts: a git hook fires on a commit or push the person chose to
make, a Claude Code hook fires the moment a session opens in a checkout. This
document is mostly about where the hook code lives and what it may run.

### Goals

- Gate a change locally before CI, layered so the expensive check runs least
  often.
- Keep a session opened in a checkout of somebody's branch from running that
  branch's Claude Code hooks.
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
- Keeping a git commit or push in a checkout of an unread branch from running
  that branch's code. The gates run the tree; building a branch is running it
  (see "Git hooks run from the tracked `.githooks/`" below).

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

### Git hooks run from the tracked `.githooks/`

`scripts/setup.sh` sets `core.hooksPath = .githooks`, the same wiring
wafflebase uses (there from `package.json`'s `postinstall`; yorkie has no
`package.json`, so `setup.sh` stays the entry point). The path is relative, so
git resolves it against the top of whichever worktree it runs in: one setting
serves every linked worktree, and a hook change reaches a clone on the next
checkout with nothing to re-run.

**This reverses an earlier design**, recorded here because the reasons for it
were real. Until 2026-10 the hooks were copied into `$GIT_DIR/githooks` and
`core.hooksPath` pointed at the copy, so a branch could not rewrite the hook
script that runs on a reviewer's machine. Because the copy pinned only *which*
script ran, not *what* it ran — `make lint` and `make verify` resolve through
the branch's `Makefile`, `.golangci.yml` and `go test ./...` — a second guard,
`.githooks/trusted-tree.sh`, refused to run either gate when the checkout
carried commits this clone had not created, judged from HEAD's and the
branch's reflog.

Both were removed, on the maintainer's call:

- **The guard blocked the everyday case.** Every agent-loop branch carries bot
  commits, and those are "not created by this clone" by construction. A
  maintainer finishing such a branch needed `YORKIE_ALLOW_FOREIGN_TREE=1` on
  every commit and every push. A guard bypassed by reflex enforces nothing,
  and the reflog rules it grew (pull, rebase, amend, cherry-pick, worktrees,
  forks) were a steady source of false refusals.
- **The snapshot went stale.** An improved hook reached a clone only when
  someone re-ran `setup.sh`, and nothing said when that was due.
- **The threat it answered is one the gates cannot avoid.** The gates exist
  to run the tree. Somebody who checks out a pull request and commits or
  pushes in it is building it; `make verify` typed by hand runs the same code.
  The safe practice is unchanged and lives where it belongs: read a branch
  before you build in it, and use `--no-verify` when you only mean to push.

**What this gives up, stated plainly.** With `core.hooksPath` in the tree, the
hook scripts themselves are branch-controlled again: a fork pull request can
rewrite `.githooks/pre-commit` to run anything, and a maintainer who checks it
out and commits runs it. That is accepted because the snapshot never removed
the exposure, only moved it — the snapshotted hooks already executed the
branch's `Makefile`, `.golangci.yml` and test code. The rule is the same in
both cases: read a fork branch, `.githooks/` included, before committing in
it.

A checkout with no `.githooks/` — an old release branch cut before the
directory existed — runs no hooks at all, since git treats a missing hooks
directory as empty. CI still gates anything pushed from it.

**Stale clones are told, and cleaned up.** A clone set up before this change
still points `core.hooksPath` at the legacy `$GIT_DIR/githooks` copy, which
keeps running the removed guard. `make lint` runs `scripts/setup.sh --check`,
which never fails the target and only warns on stderr: that the hooks are not
installed when `core.hooksPath` is unset, that it names the legacy snapshot
when it resolves (physically) to `$GIT_DIR/githooks`, or what else it names.
It is silent under `CI`. `setup.sh` itself removes the legacy copy, but only
when `core.hooksPath` currently resolves to it, and only after the
install-time source check below has passed — a refused run changes nothing.

### The Claude Code hooks keep their `$GIT_DIR` snapshot

`scripts/hooks/install.mjs`, run by `setup.sh`, copies `scripts/hooks/*.sh`
into `$GIT_DIR/agent-hooks/` (the common git dir, never a linked worktree's
`.git/worktrees/<name>`, which dies with the worktree) and writes wiring that
names **those copies** into `.claude/settings.local.json`, which is gitignored.

**Why this half did not change.** Claude Code reads project settings out of the
working tree and runs the commands they name, with no confirmation, at
`SessionStart` and before every `Edit`/`Write`. A tracked
`.claude/settings.json` would mean that opening a session in a checkout of
somebody's branch — which the maintainer loop does routinely, to review it —
runs that branch's `scripts/hooks/*.sh`, with no commit, push or build ever
chosen. That is not the "you chose to build it" argument above, and these
hooks never caused the friction that motivated the git-side change. The cost
remains staleness: an improved Claude Code hook reaches a clone on the next
`setup.sh`.

**The re-run vector.** Run inside a checkout of somebody's pull request,
`setup.sh` would make that branch's `scripts/hooks/*.sh` the clone's
checkout-proof Claude Code hooks. So before installing them it compares
`scripts/hooks`, `scripts/setup.sh` and `:(glob)scripts/*.mjs` against
`upstream/main` (or, without one, `origin/main`) and refuses when they differ,
with `YORKIE_ALLOW_LOCAL_HOOKS=1` as an explicit escape rather than a prompt.
The git hooks are enabled before that check, since nothing about them is
persisted. This guards against accident only: a hostile branch's `setup.sh`
can simply leave the check out.

**What is compared is what the script runs, not what it is named after.**
`install.mjs` imports `../direct-run.mjs`, and a list naming only the importers
once let a branch whose sole change was that module run its code. The glob
covers every sibling module a future import could reach — `:(glob)` magic
because git's default `*` spans `/` and would otherwise pull in all of
`scripts/agent/**`.

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
script the installer wires exists and is executable, and that no
`.claude/settings*.json` is **tracked** (asked of git, because `.gitignore` is
not a security control). `setup.sh` and the hooks are exercised for real in
scratch clones, with `make` and `golangci-lint` stubbed on `PATH`: `setup.sh`
wiring `core.hooksPath = .githooks` from the main checkout and from a linked
worktree, refusing changed Claude Code hook sources without changing anything,
`--check`'s warning for an unset and a legacy wiring, removing the legacy copy
only when it is wired, and a real `git commit` on a branch carrying a
bot-authored commit dispatching `pre-commit` through git and landing.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A branch's hooks or `Makefile` run when a reviewer commits or pushes in its checkout | Accepted: the gates exist to run the tree, and building a branch is running it. Read before building, `.githooks/` included; `--no-verify` to push only |
| A clone set up before 2026-10 keeps running the legacy snapshot and its guard | `make lint` warns via `setup.sh --check`; `setup.sh` removes the copy when the clone points at it |
| A checkout without `.githooks/` (an old release branch) runs no hooks | Accepted; CI gates whatever is pushed from it |
| Opening a Claude Code session in a branch's checkout runs its hooks | Wiring is gitignored `.claude/settings.local.json`, naming a `$GIT_DIR/agent-hooks/` snapshot |
| `setup.sh` re-run inside a pull-request checkout persists that branch's Claude Code hooks | Their sources compared against `upstream/main` or `origin/main`; explicit `YORKIE_ALLOW_LOCAL_HOOKS=1` to proceed |
| `setup.sh` run in a linked worktree, which is later removed | `core.hooksPath` is relative, resolved per worktree; the Claude Code snapshot lives in the common git dir |
| A future import widens the compared surface past the list | The list is `:(glob)scripts/*.mjs`, not the importers |
| The Claude Code snapshot goes stale | Documented re-run of `scripts/setup.sh`; CI is the backstop |
| A cheap gate becomes expensive and gets bypassed by reflex | Layered by measurement: lint at commit, tests at push, integration in CI |
| A gate is silently absent because its tool is missing | Refuse rather than skip wherever only an exit status is read |
| A guard stops guarding without failing | `harness-hooks.test.mjs` pins each one against this tree |
| A contributor cannot run the gates at all | `scripts/setup.sh` is opt-in and Node is optional on the Claude-hook leg; CI holds every gate unconditionally |

### Design Decisions

| Decision | Reason |
|----------|--------|
| `make lint` at commit, `make verify` at push | 5.5 s vs 35 s measured; the per-commit gate has to stay cheap enough to keep |
| `core.hooksPath = .githooks`, no snapshot, no trust guard (2026-10) | The guard refused every maintainer commit on agent-loop branches and the snapshot went stale; building a branch runs it either way. Matches wafflebase |
| Claude Code hooks keep the `$GIT_DIR` snapshot | They fire when a session opens, not on a commit or push someone chose; tracked wiring would run a reviewed branch's scripts unasked |
| `.claude/settings.local.json`, gitignored, never tracked | Claude Code runs what project settings name, unconfirmed, at session start and before every edit |
| Opt-in via `scripts/setup.sh` | Installing hooks on a clone without being asked is the behaviour being refused, one layer up |
| A Node licence checker rather than a Go tool | Node was already in the workflow, the `Docs` workflow was the right unfiltered home, and clause-matching is more forgiving than template comparison across two comment styles |
| `pre-commit` lints the working tree | An index-only checkout costs a temporary worktree per commit; `pre-push` and CI see the real tree |
| `session-prime.sh` exits silently under `GITHUB_ACTIONS` | Its content is a local multi-commit workflow; a CI fix job is told to fix what it was handed and nothing else |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Track `.claude/settings.json` so the Claude Code hooks wire themselves, as wafflebase does | Opening a session in a branch's checkout — routine when reviewing — would execute that branch's hook scripts with no confirmation and no commit or push chosen |
| Snapshot `.githooks/` into `$GIT_DIR` plus `trusted-tree.sh`, refusing a checkout with commits this clone did not create | Shipped until 2026-10, then removed. Bot commits made every agent-loop branch "foreign", so maintainers set `YORKIE_ALLOW_FOREIGN_TREE=1` on every commit and push; the snapshot needed a re-run after each hook change |
| Trust commits reachable from any `origin/*` branch and guard only fork-PR commits | Narrower, and it would stop refusing agent-loop branches, which live on `origin`. Rejected for the simpler wafflebase model: it keeps the reflog machinery and its false refusals for fork work, and guards a threat the gates cannot avoid anyway |
| Keep the guard, but trust bot authors | Author lines are written by the branch, so an allow-list is spoofable — the reason the guard moved to the reflog in the first place. More rules on a guard whose threat the gates cannot avoid anyway |
| Refuse when `Makefile` / `.golangci.yml` differ from upstream | Closes two of three paths; `go test ./...` runs every `_test.go` in the tree, and there is no file list for that |
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
[20260926-backport-hook-fixes-todo.md](../tasks/archive/2026/09/20260926-backport-hook-fixes-todo.md).
The trust guard and the git-hook snapshot were removed under
[20261009-remove-trusted-tree-guard-todo.md](../tasks/active/20261009-remove-trusted-tree-guard-todo.md).

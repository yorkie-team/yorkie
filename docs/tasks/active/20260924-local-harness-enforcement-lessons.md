**Created**: 2026-09-24

# Local harness enforcement — lessons

## The survey finding that did not survive its own file

The task list arrived from a comparison against the repository this pipeline
was ported from, and one item was "give `pick-credential.mjs` an `available`
output so `@claude fix` pages instead of reporting a drained credential pool
as *the branch head did not advance*". It looked like a small, obviously
correct fix: the sibling module `pick-fix-credential.mjs` already does exactly
that.

`pick-credential.mjs`'s own header refutes it, in a section headed **HOW THIS
DIFFERS FROM `pick-fix-credential.mjs`**: that module works by reading pool
state a review panel recorded, and "these jobs have no panel before them and
no artifact to read". It then closes the door on the general version too —
"it cannot tell whether the slot it names is live. Nothing short of spending a
call can, and a probe would cost the round trip it is trying to save."

**Rule: read the target module's header before trusting a finding about it.**
A survey compares two trees; it cannot see the reasoning that made them
differ. Here the difference was not an omission, it was an answer. The cost of
finding this out was one file read; the cost of not doing it would have been a
plausible-looking change that cannot work, defended by a reviewer's intuition
that it matches the sibling.

## The second rejection, for the same shape of reason

The upstream repository pairs the AI-disclosure commit trailer with a
`require-ai-disclosure.sh` hook that enforces it. Porting it looked like a
clean win, and `disclosure.mjs` even flags its absence: *"NO HOOK MIRRORS THIS
HERE."*

It would have been a permanent no-op. That hook is inert unless an environment
variable is set; upstream it is the local `spec-to-pr` front half that sets it,
and nothing in this repository sets it at all. The gate that actually holds
here is the PR-body predicate, which is what `disclosure.mjs` says.

The first draft of that reasoning carried a second leg — that
`claude-code-action` never reads a branch's `.claude/settings.json`. Round 2
found it unverified, and contradicted by this repository's own
`agent-review-panel.yml`, which deletes `.claude/` precisely because it holds
"settings + hooks the SDK could load and run". **A correct conclusion reached
partly by an unchecked claim is still a defect** — the next person inherits the
claim, not the conclusion. The argument now rests only on the leg that was
always load-bearing.

Two of the five candidate items were wrong. Both were caught by reading, not
by testing — a test would have passed.

## A guard that would have blocked the release procedure

`guard-generated-files.sh` started out guarding everything `buf generate`
writes, which includes `api/docs/yorkie/v1/*.openapi.yaml`. MAINTAINING.md §1
has a maintainer hand-edit the `version` field in three of those files on
every release. The guard would have refused the release procedure the
repository documents.

**Rule: for a blocking gate, enumerate what the rule would refuse before
writing it, not after.** "Generated" turned out not to mean "never edited".
The scope shrank to the Go output, and the exclusion is now stated in the hook
so the next person does not widen it back.

The same check nearly bit twice: a block comment prepended above a
`//go:build` line breaks the constraint, because build tags may be preceded
only by *line* comments. None of the 17 backfilled files carried one — verified
before the edit, not after — and the repository's own convention for tagged
files is tag first, then header.

## Fix first, then widen — the comment said so

`agent-scripts.yml` lints only `agent-*.yml`, and its comment explains that
widening the scope surfaces two findings belonging to other lanes. It does not
argue they should stay broken. It argues they should not be fixed *silently,
inside an agent change, by whoever next touches these scripts*:

> They are worth fixing; they are not worth fixing silently.

So the sequence was: verify both findings are still live using the lane's own
flags, fix each in its own commit with its own reasoning, then widen the scope
and rewrite the comment. Widening first would have meant either a red lane or
an ignore list — the two outcomes the comment was written to prevent.

A narrow scope with a *stated* reason is a decision, not a gap. The gap was
that the reason had an expiry date and nothing tracked it.

## Ported prose is a defect class, not a one-off

Three comments in this tree describe a repository that is not this one:

- `agent-review-panel.yml` cited "`ci.yml`'s `.harness-reports/` upload" as
  precedent. `ci.yml` has no `upload-artifact` step at all, and
  `.harness-reports/` here is a gitignored scratch directory
  `novelty.test.mjs` writes.
- The same paragraph attributed the bug to `#641`, which in this repository is
  "Bump checkout from v3 to v4".
- `docs.yml` justified its lack of a path filter with "packages/cli-style
  READMEs cite source files, and deleting a cited `.ts`" — a TypeScript
  monorepo layout this repository does not have.

All three are in comments, so nothing failed. That is the point: the mechanism
each paragraph protects is correct and measured, and a reader who checks the
cited evidence finds it absent and discounts the rest. **When porting, the
values inside a justification need reading as carefully as the logic above
them** — the same lesson the pipeline already learned about a bot identity
that looked like configuration and was the entire permission model.

## Measure before choosing where a gate runs

The split between `pre-commit` (lint, ~6s) and `pre-push` (lint + unit tests,
~40s) came from timing both first. Had `make lint` been a minute, the right
answer would have been different — a gate people disable is worth less than no
gate, because it also removes the expectation.

The one gate that could not be placed by cost was the licence check: it needs
Node, which nothing else in the local Go workflow does. It runs in `make
verify`, and when Node is absent it prints `SKIPPED` and names the workflow
that still checks it. A silent skip would have been the failure this
repository's own rule warns about — a check that reports nothing is
indistinguishable from a check that found nothing.

## What the harness caught while being built

The `commit-msg` hook rejected two subject lines over 70 characters during
this task, and the new `pre-commit` hook refused the deliberate `lll`
violation used to prove it works. Both gates were exercised by the commits
that introduced them.

## Review rounds

### Round 1 — weighted to correctness and test adequacy

One Critical, seven Important, ten Minor. All eight blocking findings
fixed; the rejections and deferrals are recorded below.

The Critical one is the lesson. Adding the licence lane was half the
change: **four places in the tree still asserted the gap was open**, and
the worst of them is `MECHANICAL_COVERAGE_NOTE` in `review-panel.mjs`,
which is appended last to every lens prompt. Every lens on every panel
run was being told that a finding about a missing licence header is
"worth MORE than one the lanes above would have caught" — directed to
spend turns on a class `docs.yml` now reds in seconds. And
`review-panel.test.mjs` *asserted* the stale sentence, so the guard
written to keep the note honest would have failed on the correction.

**Rule: closing a gap means finding everything that describes it.** A
grep for the gap's own words is the cheapest possible step and it was
not taken. The note's header states the failure mode in both directions
— "tell a lens something is covered when it is not and that whole
finding class stops being reported" — and the inverse costs the same.

### The lesson I wrote and then broke in the same branch

This file already argued that ported prose is a defect class. The commit
that fixed one instance (`docs.yml`'s reference to a TypeScript monorepo)
replaced it with a **fresh claim I had not checked**: that `docs/design/`
cites Go files, so deleting a cited `.go` breaks a link. Walked with the
checker's own `linkTargets`, the graph has zero relative `.go` targets —
every Go citation is an absolute GitHub URL, which the checker skips.

The true evidence was one command away: 35 images under
`docs/design/media/`, three script directories, and the workflow file
itself. Writing a justification is not the same act as checking one, and
knowing the failure class does not protect you from it — the second
version was written with the lesson already on disk. Measurements now go
in the comment as numbers, which is harder to fake than an adjective.

### The silent pass that arrived by a second door

The new checker returned `[]` for an empty tree and printed "Every Go
file carries the Apache 2.0 header", which is true of zero files. The
same Makefile comment three commits earlier had argued that a check
reporting nothing is indistinguishable from a check finding nothing.

Chasing it found the same failure by another route, in both verify
scripts: `isDirectRun` compared `process.argv[1]` to `import.meta.url`
as strings, and the loader resolves one through symlinks while the
caller's spelling is not resolved at all. Invoked as `/tmp/...` on macOS
— a link to `/private/tmp` — the CLI block never ran and the process
exited 0 in silence. **A principle stated in a comment is not a
property of the code.** Both now realpath both sides, and the success
line carries the file count so a collapsed scan is visible.

### Guards are not optional for the gate you are adding

Round 1's sharpest procedural finding: the inline-help fix shipped with
two guards, and Gate 1 — the actual subject of the task — shipped with
none. The nine payloads proving the hook works were run by hand and
thrown away, so the reviewer had to re-derive them.

`scripts/test/harness-hooks.test.mjs` now pins the three that fail
silently: the guard hook fails OPEN, so a `case` pattern that stops
matching stops guarding without erroring; a renamed hook makes its
settings.json entry a no-op; and deleting the `docs.yml` step leaves
`make verify` printing SKIPPED where Node is absent. The generated set
is derived from the tree rather than listed, so `api/yorkie/v2/` is
covered the day it appears. Seven mutations, seven failures.

### Round 2 — weighted to design fit, simplification and blast radius

No Critical. Three Important, nine Minor; all fixed. The design-fit verdict
was that the shape is right — Node in `make verify` is not a new dependency in
spirit, and the `Docs` workflow was the correct home for an unfiltered check.

**The one that mattered was blast radius, and it was mine.** To justify not
porting a hook, `disclosure.mjs` claimed `claude-code-action` never reads a
branch's `.claude/settings.json`. I had not checked it, and this repository's
own `agent-review-panel.yml` strips `.claude/` on precisely the opposite
assumption — "settings + hooks the SDK could load and run". Four workflows run
that action against the branch *without* stripping it. So the branch may have
been handing every CI fix job a SessionStart hook telling it to write a task
document and run a self-review loop, against a prompt that says "fix the
findings and nothing else".

Two lessons, and the second is the sharper one:

1. **Tracking `.claude/settings.json` is not a local-only change.** The
   directory was previously `commands/` and `skills/` — readable, inert.
   Adding executable hook wiring to it changes what a branch can do to a job
   that checks it out. `session-prime.sh` now exits under `GITHUB_ACTIONS`,
   which settles the question whichever way the underlying answer falls;
   `guard-generated-files.sh` deliberately does not, because refusing a
   hand-edit to a generated file is as right in CI as it is locally.
2. **A correct conclusion reached partly by an unchecked claim is still a
   defect.** The rejection was right — nothing in this repository sets the
   variable that hook needs. But the unchecked leg was written down in three
   places, and what the next reader inherits is the claim, not the conclusion.
   The repair is not "add a caveat": it is to delete the leg and let the
   argument stand on the one that was load-bearing all along.

### A skip nobody reads is a pass

`make verify-license` announces `SKIPPED` when Node is absent — the right
behaviour for a person who typed the command and sees the line. `pre-push`
runs `exec make verify` and git reads only the exit status, so that line
scrolled past under 35 seconds of test output and the push succeeded with the
gate silently absent. **Where a check is consumed decides whether announcing
is enough.** The hook now refuses, which also matches what already happened
for a missing `golangci-lint`.

The mirror-image finding landed the same round: `pre-commit` refused *every*
commit without `golangci-lint`, including documentation-only ones, and
CONTRIBUTING.md defended it with an argument about a missing linter. "Nothing
to lint" and "no linter" are different conditions; answering both by refusing
meant an outside contributor fixing a typo needed the Go toolchain to commit.

### Round 3 — weighted to security, documentation and design-doc consistency

No Critical. Two Important, eleven Minor; all fixed.

**The security question resolved in the branch's favour, more strongly than
the branch argued it.** Tracking `.claude/settings.json` grants a PR branch no
capability it did not already have: the three workflows that check out an
untrusted branch without stripping `.claude/` already hand the agent an
unrestricted `Bash` tool, and all three are gated to same-repo branches. The
two workflows that *restrict* the agent to read-only lenses are exactly the two
that `rm -rf .claude`. The design was coherent before and after. Worth
recording because round 2 had left this as an open worry and the honest answer
turned out to be better than the cautious one — the `GITHUB_ACTIONS` exit in
`session-prime.sh` stays anyway, because it costs nothing and the question of
what a CI agent should be *told* is separate from what it can *do*.

**The gate had a second silent-skip hole, in the half added to close the
first.** `pre-commit` filtered staged paths with `--diff-filter=ACM`, which
drops `R` — and git reports a rename-with-edit as one `R` entry above ~50%
similarity. Such a commit stages Go and skipped the lint entirely. The obvious
repair, `ACMR`, is also wrong: it drops `D`, and deleting a Go file breaks
compilation for everything that referenced it. The filter is gone.

**Rule: when you narrow a check's input, enumerate what the narrowing
excludes.** Both narrowings here read as obviously safe and neither was. The
same reflex that caught the OpenAPI bundles before the guard shipped did not
fire on a `--diff-filter` flag, because a flag does not look like a policy.

### The defect class this branch could not stop committing

Round 1: four places asserting a gap the branch had closed. Round 1 again: a
replacement justification with no relative `.go` targets behind it. Round 2: an
assumption about `claude-code-action` contradicted by this repository's own
workflow. Round 3: a security note claiming "everything else here is pinned:
actions by SHA" — two of seventeen are, and the file it was written in has
eight, all tags.

Four instances, across three rounds, in a branch whose lessons file names the
class in its own heading. Writing a justification is a different act from
checking one, and the gap between them does not close by knowing about it. What
did work, eventually: **numbers**. A claim carrying a measured count is one a
reviewer can falsify in a single command, and three of the four were caught
exactly that way. The counts that then went stale were the ones nothing
recomputes — so they are gone, and the breakdowns that do not move are what
remain.

### Outcome of the bounded loop

All three rounds raised blocking findings, so the loop ends at its bound rather
than at a clean round. Per `.claude/commands/self-review.md` that means stop,
open the PR, and get a human — not run a fourth round. Recorded here rather
than silently continuing, because "three rounds, still finding things" is
information a reviewer should have.

### The guard that only worked on my machine

CI was red on the first push, and the failure was in the two guards added to
close round 3's gate hole — not in the hook. They passed locally and failed on
the runner.

`wouldLint()` drove the real `pre-commit` script with `exec make lint` replaced
by a marker, then asked whether the marker was printed. But the hook refuses
*before* that line when `golangci-lint` is missing, and the `Docs` workflow
that runs this suite is a Node-only job with no Go toolchain. So the probe was
measuring **"could lint"** where it claimed to measure **"decided to lint"**,
and the two happen to agree on a developer machine and disagree on the runner.

The fix puts a stub `golangci-lint` on `PATH` rather than deleting the check,
so the hook runs exactly as written, and adds the case the stub makes possible:
Go staged with no linter must still refuse. "Nothing to lint" and "no linter"
must keep producing different answers, which is the distinction round 2
installed and this test was quietly erasing.

**Rule: a test of environment-dependent behaviour has to be run in the
environment.** `env -i PATH=<node>:/usr/bin:/bin` reproduces the runner in a
second and would have caught this before the push. It is now how these guards
are checked: 48/48 under that PATH, and both narrowings of the filter still
fail under it.

The irony is on the nose — the branch exists because a gate that quietly does
nothing is indistinguishable from a gate that found nothing, and its own new
guard did exactly that, in the direction that reports failure rather than
success only because the runner lacks a binary.

### Round 4 — the review panel, and a round-3 conclusion reversed

Three blocking findings, all fixed; no rebuttals.

**The security answer from round 3 was right about the threat it examined and
wrong about the one it did not.** Round 3 asked whether tracking
`.claude/settings.json` grants a *PR branch* new capability *in CI*, and
answered no: the workflows that check out an untrusted branch without
stripping `.claude/` already hand the agent an unrestricted `Bash` tool. That
still holds. The panel asked about the other consumer — a **person** who checks
out a contributor's branch to review it. There, tracked wiring plus tracked
`scripts/hooks/*.sh` means opening a session in that checkout runs the
branch's code at SessionStart and before every Edit, with no confirmation and
no CI sandbox around it. Reviewing a patch became running it.

The repair inverts both halves: the wiring moves to the gitignored
`.claude/settings.local.json`, and `install.mjs` snapshots the hook scripts
into `$GIT_DIR/agent-hooks/` — a directory `git checkout` never writes — so a
branch supplies neither the wiring nor the code. The cost is staleness: an
improved guard reaches a clone only when someone re-runs `scripts/setup.sh`.
That is the right direction to fail, and CI's codegen-freshness check remains
the backstop the guard was only ever an accelerator for.

**Rule: "does this grant the attacker a new capability?" has to name the
victim.** Round 3's analysis was sound for the CI agent and never asked about
the laptop. A capability question with an unstated subject answers itself with
whichever subject is most convenient.

**A refusal surface has to cover what its members import.** Gate 1b listed
`scripts/verify-*.mjs` because a workflow invokes those directly — but both of
them now import `direct-run.mjs` for the predicate deciding whether their CLI
body runs at all, so editing that one unlisted file makes both of `docs.yml`'s
gates exit 0 having checked nothing. The pattern is now `scripts/*.mjs`.
Extracting a shared helper moved the CI definition out from under the glob
that named it, in the same change that made the extraction worth doing.

**Widening a linter's scope means owning everything newly in it.** Pointing
actionlint at the whole workflow directory was justified in a comment that
accounted for only the two files that had *reported*. The rest were newly
graded too, silently: actionlint is clean on all of them (verified by running
the lane's exact image, `rhysd/actionlint:1.7.12`, over the full directory),
but it has no view of an action's runtime, so `chart-release.yml`'s node16
`azure/setup-helm@v3` is newly in scope and still unchecked.

**LEFT UNDONE, AND NOT BECAUSE IT WAS JUDGED WRONG.** The fix is a one-line
bump of `.github/workflows/chart-release.yml:26` to `azure/setup-helm@v4`
(same `token` input, node20 runtime) plus the scope comment in
`.github/workflows/agent-scripts.yml`. The autonomous fixer's GitHub App token
carries no `workflows` permission, so the push was rejected outright —
`refusing to allow a GitHub App to create or update workflow ... without
'workflows' permission`. A maintainer has to make both edits. Recorded here
rather than quietly dropped: a change a bot cannot push is invisible in the
diff and looks exactly like a change nobody thought was needed.

### Round 4, continued — the two the fixer could not close

The autonomous fixer's own lessons entry above is accurate and its repair is
better than the one this session had planned (CODEOWNERS on `.claude/`, which
would have made the hook body a reviewed file without making it an unreachable
one). Verified rather than accepted: after `scripts/setup.sh`, rewriting
`scripts/hooks/guard-generated-files.sh` in the worktree to `echo PWNED` leaves
the installed snapshot under `$GIT_DIR/agent-hooks/` still refusing an edit to
a `.pb.go`. The mechanism also resolves `$GIT_DIR` correctly for a submodule
checkout, where it is not a `.git` directory in the worktree.

**The disputed finding.** Blast-radius raised `chart-release.yml`'s
`azure/setup-helm@v3` as "if actionlint's popular-actions data covers
azure/setup-helm, the lane reds on every PR", and said outright it could not
run actionlint to check. Run: `rhysd/actionlint:1.7.12` with the lane's own
flags reports nothing across all workflows, and a planted file containing both
`azure/setup-helm@v3` and `codecov/codecov-action@v3` reports only the second.
The stated consequence is false.

The observation underneath it is not, and the fixer had already reframed it
correctly: actionlint reads inputs and expressions, never an action's runtime,
so a `node16` action is newly in scope and permanently invisible to the lane
grading it. Bumped to v4 here — inputs compatible, `token` dropped because v4
defaults it and marks it deprecated.

**Rule: separate a finding's claim from its consequence before disputing it.**
The consequence was checkable in one command and wrong; the claim was right and
would have been thrown out with it. A rebuttal that answers only the loudest
sentence loses the finding.

**What a bot cannot push is not the same as what nobody needs.** Both edits
live in `.github/workflows/`, which the agent App has no permission to write,
so the fixer's diff shows nothing and reads as a judgement that nothing was
needed. It said so explicitly instead. That is the honest shape for a refusal
the tooling imposed rather than the reviewer.

### Round 5 — closing the seam between two threat models

The panel blocked on the seam, and it was right: `install.mjs` inverted its
whole design so a branch could not supply code that runs on a reviewer's
machine, while `setup.sh` two files away pointed `core.hooksPath` straight at
the tracked `.githooks/`. A pull request rewrites `pre-commit`, a reviewer
checks the branch out and commits anything, and it runs — reaching the
branch's Makefile and Go test code through `make lint` and `make verify`.

The exposure predates this change (`commit-msg` was already tracked and
already executed), but this change is what made it inconsistent: one hook
system hardened, its twin left open, in the same commit range.

Now symmetric. `setup.sh` copies `.githooks/` into `$GIT_DIR/githooks` and
points `core.hooksPath` there. Verified the same way: rewriting
`.githooks/pre-commit` in the worktree to `exit 1` does not block a commit,
because the snapshot is what runs.

**Rule: when you harden one instance of a pattern, enumerate the others in the
same change.** The argument that justified the installer applied word for word
to the git hooks, and nobody noticed for two rounds because the two live in
different files and are installed by different code.

### Two findings refuted the same way, twice

Round 4 blocked on `bufbuild/buf-lint-action@v1` possibly tripping actionlint's
"too old" rule — "verifier: confirmed, low confidence", and the finding said it
could not run actionlint. Planted a probe carrying both it and
`codecov/codecov-action@v3`: only the second is reported. Round 3 had produced
the identical shape for `azure/setup-helm@v3`.

Security likewise flagged the codecov SHA pin as "asserted, not verified — a
SHA from a fork resolves identically". Checkable: `0fb7174895…` is a commit in
`codecov/codecov-action` itself, message `chore(release): 5.5.5`, and it is
exactly what the `v5` tag dereferences to.

**Rule: a finding that says it could not check something is a task, not a
verdict.** Both were one command away. The panel is right to raise what it
cannot confirm — the error is leaving it unconfirmed on the other side too.

### A skip list is an allowlist for the thing it skips

Widening `SKIP_DIRS` to cover the sibling checkouts CI stages (`repo`,
`benchmark-repo`, `load-repo`, `.trusted*`) was correct and nearly introduced a
hole: those are ordinary words, and the walk matched at any depth. A future
`pkg/repo/` would have left the licence gate silently — the exact failure the
file exists to refuse, bought for nothing, since every one of them is staged at
the root or not at all.

Split in two: `.git`/`node_modules`/`vendor` at any depth, because they are
certain wherever they sit; everything else at the root only. Both halves are
tested, and making the root-only set match at any depth fails the suite.

### The fixer cannot push the fix for a workflow finding

Round 5's panel raised two findings whose only possible fix is a workflow edit,
and the autonomous fixer holds an App token minted with `contents: write` and
nothing else. The push is rejected outright, for the whole ref:

```
! [remote rejected] refusing to allow a GitHub App to create or update
  workflow `.github/workflows/agent-iterate-ci.yml` without `workflows`
  permission
```

That is the boundary `agent-review-panel.yml`'s `workflow_run` trigger rests on,
so it is working as designed — but the loop has no way to say "authored,
unpushable", and a finding nobody can act on is re-raised every round. Both
changes are written out below for a maintainer to apply; the security finding in
the same round needed no workflow file and shipped.

**1. The licence gate must also run in `ci.yml`.** `agent-iterate-ci.yml`
subscribes to `workflow_run: workflows: ["CI"]` and asserts the run's path is
`.github/workflows/ci.yml`; the panel's `promote`/`fix` jobs read CI's
conclusion alone. A blocking gate that exists only in `docs.yml` therefore reds
an agent-managed PR with nothing to observe it, nothing to re-trigger and no
page — it just stops. `docs.yml` keeps its copy (it is the unfiltered lane);
`ci.yml` gets a second one, after `make lint`:

```yaml
      - name: Verify licence headers
        run: node scripts/verify-license.mjs
```

`node …` rather than `make verify-license`, because that target announces
SKIPPED when node is absent and a CI gate must not fail open. Pin it with an
assertion in `scripts/test/harness-hooks.test.mjs` next to the `docs.yml` one,
or deleting the copy silently restores the dead-end.

**2. `bufbuild/buf-lint-action@v1` is archived and now in actionlint's scope.**
Widening `agent-scripts.yml` from `agent-*.yml` to the whole directory put every
third-party action in a lane that fails on an aged-out runtime — which is how
`codecov/codecov-action@v3` was caught. actionlint 1.7.12 does not flag
buf-lint-action today (verified: exit 0 over the whole directory), so this is
pre-emptive, and the reason to do it early is that the failure would land on
whichever PR next touched a workflow, where no autonomous fixer can repair it.
Replace the step with the CLI `buf-setup-action` already installs:

```yaml
      - name: Lint proto files
        run: buf lint
```

`buf.work.yaml` points the workspace at `api/`, which is how the `buf breaking`
and `buf generate` steps beside it already resolve.

**Rule: check what the token can push before choosing where to fix.** The fix
that is right in the abstract and the fix that can land are not always the same
file, and a round spent authoring an unpushable diff buys only the record of it.

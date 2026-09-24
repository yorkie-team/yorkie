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

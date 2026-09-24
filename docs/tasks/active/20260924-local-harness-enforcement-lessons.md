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

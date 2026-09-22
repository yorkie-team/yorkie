# Lessons: installing the advisory `@claude` command surface

**Created**: 2026-09-22

## The verbs are not the unit of adoption

The four commands this started from — `fix`, `loop`, `review`, `rerun` — read
like four independent features. Three of them are satellites of one review
panel: `loop` applies the label the panel's gate admits a PR on, `rerun` clears
a latch the panel sets, and `fix` is eligible only when the current head SHA
already carries the panel's lens check runs. Porting any of those without the
panel installs a no-op that looks installed.

`review` was the one separable verb, which is what made Phase 1 possible at all.

## The language barrier was not where it looked

A Go repository adopting a TypeScript monorepo's tooling sounds like the hard
part. It was not: `scripts/agent` is a standalone npm package with its own
lockfile that nothing in the Go build reaches, and this repository already runs
`node --test` in CI for the doc-link checker. The workflows carried exactly one
repository-specific line between them — a `git diff` pathspec excluding
generated files.

What actually needed rewriting was the knowledge, not the code: which paths mean
what, and what CI already proves.

## The coverage note is the dangerous file

`MECHANICAL_COVERAGE_NOTE` tells each lens what the mechanical lanes already
catch, so it does not spend turns re-deriving them. Its failure mode is silent
in one direction only: claim something is covered when it is not, and that whole
finding class stops being reported with nothing in the output to show for it.

Upstream's copy says formatting is unchecked (Prettier is write-only there).
Here `.golangci.yml` enables `gofmt` and `goimports` as formatters, so that
bullet is not merely stale — it is backwards. In the other direction, the same
file *disables* `staticcheck` and `unused`, so a bare "golangci-lint runs" would
have implied both. Two claims, two opposite errors, from one file translated
instead of re-read.

The test that pins each claim was rewritten alongside it, which is the only
thing that keeps the note honest as the lanes move.

## Skip a guard, do not delete it

Four ported tests assert that a module and a workflow carry the same literal —
the paged-latch marker, the inline finding-selection copy, the freeze-point
wiring, the upload steps that would silently upload nothing without
`include-hidden-files`. Their workflows arrive in later phases.

Deleting them would have been quieter and worse: every one of those guards
exists because the drift it catches is invisible, so removing them makes Phase 2
a silent regression of exactly the checks written to prevent one. They skip
through a single helper that names the reason, and re-arm the day the workflow
lands.

The same reasoning kept `rounds.mjs` and `fix-report.mjs` in the tree even
though no Phase 1 code path reaches them: `review-panel.mjs` imports both
directly, and surgery inside a ported 3,400-line module is what makes the next
sync from upstream expensive.

## An inherited claim that could not be checked here

Upstream mints a GitHub App token to post its comments, stating that a
fork-PR-triggered run's `GITHUB_TOKEN` is forced read-only. An `issue_comment`
run executes in the base repository with the permissions its job declares, so
`issues: write` should be enough — but "should" is not a test, and there was no
fork PR to try it on.

Rather than pick a side, the placeholder step is `continue-on-error` and the
publish step already creates a fresh comment when no placeholder id reaches it.
A wrong guess costs the "running…" note, not the review. Worth remembering as a
shape: when a ported claim cannot be verified in the new repository, find the
arrangement where being wrong is cheap instead of arguing about it.

## `for m in $VAR` does not split in zsh

Two copy loops silently did nothing, and one of them reported "no tests exist"
for files that were right there. Cost: a wrong conclusion about the upstream
tree that survived two follow-up commands before `ls` contradicted it. In zsh,
word splitting on unquoted parameters is off by default — use an explicit list
or an array.

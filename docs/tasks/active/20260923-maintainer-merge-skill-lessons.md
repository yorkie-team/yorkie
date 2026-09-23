**Created**: 2026-09-23

# Maintainer Merge skill — lessons

## The baseline did not fail, and that changed the skill

The premise was that an agent asked to merge a PR here would guess at the
gates. It did not. Given the repo and `gh`, a fresh agent with no skill
derived the branch protection, the squash-only setting, the
`COMMIT_MESSAGES` default body, the empty `contexts`, and that `license/cla`
is a commit status rather than a check run. It found things this session had
missed while merging #2025 for real: `--match-head-commit`, that
`dismiss_stale_reviews: false` lets an approval survive a force-push, and
that `ci.yml`'s `paths-ignore` makes a docs-only PR produce no Go checks at
all.

Writing-skills says to stop when the control does not exhibit the failure.
The honest reading is that it exhibited a *narrow* one: three gaps, not a
wholesale miss. So the skill shrank to those gaps plus a settings table,
and its Overview says outright that the rest is derivable. A skill that
restates what an agent can already work out is a skill nobody needs to read.

The remaining argument for writing it at all is cost, and it should be named
as such rather than dressed up as correctness: the baseline spent ~56K tokens
and 20 tool calls rederiving it, and will again next time.

## What the baseline actually got wrong

Two commits on `main` carry no `(#N)` suffix. The baseline concluded they
were pushed straight to `main` in violation of `CLAUDE.md`. They were not —
`gh api .../commits/<sha>/pulls` shows #2022 and #2023. It inferred a policy
violation from a formatting difference and stated it as fact.

That is the failure worth encoding, and it generalises past this repo: an
absent marker is evidence about the marker, not about the process. The
pitfall table now carries the one-command check.

The same observation settled a question the other way round: because those
two landed without the suffix, `--subject` must be passed through verbatim.
GitHub does not append `(#N)` when you supply the subject explicitly — which
is why the ≤70 budget is for the text before it, and why subjects on `main`
reach 78 characters.

## The GREEN run found two errors in the skill

Worth recording because the skill was written by the same person who had
just done the merge by hand, which is exactly the position that produces
confident wrong statements:

- `strict: true` was missing from the table. It is the reason a stale branch
  has to be rebased before it can merge, and the one gate `--admin` would
  silently bypass — merging a combination nothing tested.
- "`--delete-branch` is redundant" is true only for same-repo PRs.
  `delete_branch_on_merge` cannot touch a fork, so the contributor's branch
  survives the merge.

Neither would have been caught by re-reading. A second agent applying the
skill to a scenario the author had not run is what surfaced them.

## Rounds run

- RED: one run, no skill, general merge scenario. Gaps recorded above.
- GREEN: one run, with the skill, on the harder scenario the skill claims to
  cover (fork + behind `main` + touches `.github/workflows`). All three gaps
  closed; the two errors above surfaced.
- REFACTOR: applied. **Not re-tested.** The changes are verified facts the
  GREEN run itself supplied, and one correction it made, so a third round
  would mostly re-derive them — but that is a judgement, not a clean round,
  and it should not read as one.

Single samples throughout; writing-skills asks for five or more. The skill is
reference material rather than discipline enforcement, so the wording is not
load-bearing under pressure, but the sample size is what it is.

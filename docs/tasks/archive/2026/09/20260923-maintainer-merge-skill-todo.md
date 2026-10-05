**Created**: 2026-09-23

# Maintainer Merge skill

Add a repo skill documenting how a yorkie maintainer squash-merges a PR:
what to verify before merging, how the squash message has to be shaped, and
which of this repository's settings decide the path.

## Motivation

Merging #2025 surfaced facts that are not discoverable from the PR page and
that an agent asked to "merge this" will otherwise guess at:

- `main`'s branch protection declares **no** required status checks
  (`required_status_checks.contexts == []`). A PR sitting at
  `mergeable=MERGEABLE, mergeStateStatus=BLOCKED` is blocked on the required
  *review*, not on a check. Waiting for CI to unblock it waits forever.
- `enforce_admins == false`, so `--admin` is available — but only knowing
  that makes it a decision rather than a guess.
- `required_status_checks.strict == true`: the branch has to be current with
  `main` before it can merge, so a PR that sat through review needs a rebase.
- The head branch is often a fork. Pushing to it needs
  `maintainer_can_modify` and a push by URL, since no remote is configured.
- The squash subject goes through the same constraints as any commit here:
  ≤70 chars (the `commit-msg` hook rejects longer), verb-first, no type
  prefix, and it has to end with `(#N)` to match the rest of `main`.

## Approach

The skill encodes what this repository's settings actually are, so the
procedure is a check-then-act sequence rather than a recital of `gh` flags.
Scope is any yorkie PR merge, not only fork or agent-pipeline ones.

Deliberately out of scope: folding the `<!-- agent-metrics-summary -->`
effort block into the squash body. wafflebase does that; no commit on
yorkie's `main` carries one, and adopting it silently through a skill would
introduce a convention nobody agreed to.

## Checklist

- [x] RED: baseline scenario without the skill. It derived most of the
      procedure unaided, so the skill shrank to what it missed.
- [x] Write `.claude/skills/maintainer-merge/SKILL.md` against those gaps:
      fork-branch push, the `workflow` token scope, and when `--admin` is the
      honest instrument.
- [x] GREEN: re-run on the harder scenario the skill claims to cover. All
      three gaps closed.
- [x] REFACTOR: folded in `strict: true` and corrected the fork-branch
      deletion claim, both surfaced by the GREEN run. Not re-tested — see the
      lessons file.
- [ ] Open the PR.

## Review

The skill is 930 words, above the 500 that writing-skills suggests. Every
line is either a verified setting or a gap the baseline demonstrably had, and
the Overview states outright that the rest of the procedure is derivable — so
the length buys facts rather than restatement. Trimming further would mean
dropping verified settings and sending the reader back to the API calls the
file exists to save.

Deliberately not included: the `<!-- agent-metrics-summary -->` effort block
in the squash body. wafflebase folds it in; no commit on yorkie's `main`
carries one, and a skill is the wrong place to introduce a convention nobody
agreed to.

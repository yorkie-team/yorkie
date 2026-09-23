---
name: maintainer-merge
description: Use when merging a yorkie pull request as a maintainer — a fork PR that needs a rebase or conflict fix pushed to its branch, a PR touching .github/workflows, a PR sitting at mergeable=MERGEABLE with mergeStateStatus=BLOCKED, or a merge you have been asked to push through with maintainer privileges.
---

# Maintainer Merge

## Overview

Most of this procedure is derivable: `gh pr view`, `gh pr checks`,
`gh api repos/yorkie-team/yorkie/branches/main/protection`, and
`.githooks/commit-msg` tell you the state, the gates and the message rules.
Derive those. **This file carries only what the repository cannot tell you**,
plus the settings worth knowing before you spend calls rediscovering them.

**Core principle: check the settings, and report what you actually did.**

## Settings that decide the path

Verified 2026-09-23. Re-check if a merge behaves unexpectedly —
`gh api repos/yorkie-team/yorkie --jq '{squash:.allow_squash_merge,merge:.allow_merge_commit,auto:.allow_auto_merge}'`
and the `branches/main/protection` endpoint.

| Setting | Value | Consequence |
|---|---|---|
| `allow_squash_merge` | true, and it is the **only** one | `-m`/`-r` are rejected by the API |
| `allow_auto_merge` | false | `--auto` is unavailable; wait, or `gh pr checks --watch` |
| `squash_merge_commit_message` | `COMMIT_MESSAGES` | The default body is every commit message concatenated. Pass `--body-file` |
| `required_status_checks.contexts` | `[]` | **Nothing is required.** A red PR will merge. Read `gh pr checks` yourself |
| `required_status_checks.strict` | true | Up-to-date is still required. A behind branch reports `BEHIND` and the merge refuses — rebase it. `--admin` would bypass this and merge a combination nothing tested |
| `required_approving_review_count` | 1 | `MERGEABLE` + `BLOCKED` means the review is missing, not a check |
| `require_last_push_approval` | false | A force-push does not dismiss the approval. Pass `--match-head-commit <newSha>` |
| `enforce_admins` | false | `--admin` is available — see below before using it |
| `delete_branch_on_merge` | true | Redundant for same-repo PRs. **Does nothing for a fork** — GitHub only auto-deletes in its own repo, so the fork branch survives; tell the contributor |

## Fork PRs

The PR branch lives on the contributor's fork and **no remote points at it**.
To push a rebase or a conflict fix there:

```bash
gh api repos/yorkie-team/yorkie/pulls/<N> -q '.maintainer_can_modify, .head.repo.full_name, .head.ref'
git fetch git@github.com:<forkOwner>/yorkie.git <head.ref>
git checkout -B pr-<N> FETCH_HEAD && git rebase origin/main
git range-diff origin/main <oldSha> HEAD    # prove you rebased, not rewrote
git push --force-with-lease=<head.ref>:<oldSha> git@github.com:<forkOwner>/yorkie.git HEAD:<head.ref>
git ls-remote git@github.com:<forkOwner>/yorkie.git refs/heads/<head.ref>
```

`maintainer_can_modify` must be true; if it is false, stop and ask the
contributor to rebase. Always confirm with `ls-remote`: SSH can linger after a
successful push and time the command out, and a `commit-msg` hook that rejected
a conflict-resolution commit leaves you force-pushing a no-op. The remote SHA
must be your new commit.

## PRs touching `.github/workflows/*`

`gh pr merge` fails with *refusing to allow an OAuth App to create or update
workflow … without `workflow` scope*. It is a property of the account, not the
repo, so check yours: `gh auth status | grep -i scopes`.
`gh auth refresh -h github.com -s workflow` fixes it but is interactive — it
cannot run inside a tool call, so hand it to the human. The fork push above
goes over SSH and needs none of this.

## Merging past the required review

`--admin` works because `enforce_admins` is false. It is legitimate when the
maintainer has decided to merge and says so. It is not a default.

Do not reach for `gh pr review --approve` to clear the gate instead: that
records a review that did not happen. `--admin` records what is true — a
maintainer bypassed the requirement. Prefer a real approval from a second
maintainer when one is available; you cannot approve your own PR.

## Squash message

The message rules (subject ≤70, blank line 2, body ≤80) are in `CONTRIBUTING.md`
and `.githooks/commit-msg`. Two things neither tells you:

- **The hook does not run on a GitHub-side squash.** Nothing validates the
  message server-side. Run it yourself against the subject **without** the
  `(#N)` suffix plus the body — feed it the suffix and it rejects a perfectly
  conventional subject:
  `{ echo "<subject>"; echo; cat body.txt; } > /tmp/m && .githooks/commit-msg /tmp/m`
- **`--subject` is used verbatim — GitHub does not append `(#N)`.** Include it
  yourself. `70ec0666` and `752a9831` landed without it because someone did
  not. The ≤70 budget is for the part before the suffix; landed subjects on
  `main` run to 78 with it.

## Pitfalls

| Symptom | Cause / Fix |
|---|---|
| `MERGEABLE` but `BLOCKED` | The required review, not a check. `contexts` is empty |
| Waiting for CI to unblock the PR | It never will — no check is required here |
| A commit on `main` without `(#N)` | Not evidence of a direct push. Check `gh api repos/yorkie-team/yorkie/commits/<sha>/pulls` before saying so |
| Docs-only PR shows no `build`/`bench`/`complex-test` | `ci.yml` has `paths-ignore`; expected, not a failure |
| Push to the fork "succeeded" but the head is unchanged | The commit never happened (hook rejected the subject). Fix and repush |
| Merged something newer than what was reviewed | `require_last_push_approval` is false. Pass `--match-head-commit <headRefOid>` |
| The PR edits a workflow that never ran on it | `issue_comment` and `workflow_run` workflows execute from the default branch, so a fork's edit is not exercised before merge. Check `gh run list` after merging — a startup failure shows as a nameless, job-less run |

## Quick reference

```bash
gh pr view <N> --json mergeable,mergeStateStatus,reviewDecision,headRefOid,isCrossRepository,files
gh pr checks <N>
gh pr merge <N> --squash --admin \
  --subject "<verb-first, ≤70> (#<N>)" --body-file <path> --match-head-commit <headRefOid>
```

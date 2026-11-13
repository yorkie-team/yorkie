# Tell a copy of the shared agent harness when it is behind

**Created**: 2026-11-14

## Problem

A repository that copied the shared harness files learns that the repository
they came from has moved on through a warning annotation on a green weekly
run. Nobody opens a green run, so nobody is told.

## Goal

In a copy, the weekly run opens one issue when upstream's default branch has
a different manifest, keeps it current, and closes it when the copy matches
again. It only notifies. A person runs `sync.mjs`.

## Plan

- [x] `upstream-notify.mjs`: decide from the recorded commit and digest, the
      head of upstream's default branch and the manifest at that head; open,
      rewrite or close one issue; `--dry-run` sends only `GET`s
- [x] The pinned check failing, or the head being unreadable, throws before
      this repository's API is called: a wrong record is not "upstream moved"
- [x] The issue is found by author (`github-actions[bot]`) and a marker on
      the first line of its body, never by title or label
- [x] Everything written into the issue is a validated slug, commit id or
      digest; nothing upstream wrote is copied in
- [x] `agent-upstream-notify.yml`: schedule and dispatch only, `main`
      checked out, `contents: read` and `issues: write`, the job's own token
- [x] Trust guard: a job that runs the notifier is held to those triggers,
      those two permissions and no secret; ten mutation cases
- [x] `manifest.mjs`: the retry is shared, and a passing check hands back the
      default branch's name
- [x] README: the fourth row, and what the notifier does and does not do
- [x] `npm test`, `npm run test:repo`, `npm run check:upstream`,
      `manifest.mjs verify`, actionlint, `scripts/test`, `make verify`
- [x] One dry run against the public API as a made-up copy of
      `yorkie-team/yorkie` at a commit on `main`: the manifest is not on
      `main` yet, the pinned fetch returned 404, and it failed without
      reaching the copy's API
- [ ] A run on a runner, in a repository that is a copy: open, rewrite,
      close, and the label fallback
- [ ] Review rounds on the pull request

## Not in this change

- A sync pull request. A sync ends in configuration edits and a test run
  that only the adopting repository can judge.
- Assignees, a dry-run input on the dispatch, any configuration key.
- A private upstream: the job's token reads public repositories.
- The warning in `agent-scripts.yml` stays. It costs nothing and is on the
  run a maintainer is already reading when the third check goes red.

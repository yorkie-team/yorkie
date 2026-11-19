# File out-of-diff follow-ups once per merged PR

**Created**: 2026-11-19

## Context

The review panel files every finding the out-of-diff gate demotes as its own
`agent:follow-up` issue, on every round, while the PR is still open
(`follow-up-issues.mjs`, called from `agent-review-panel.yml`). From 2026-10-04
to 2026-11-19 that filed 93 issues from 39 PRs (10 from #2196 alone). Of the 54
closed, 29 were closed without a fix. The 26 `not planned` closures break down
as:

- 10 duplicate another follow-up: the same root cause seen from another lens or
  at another file (#2302/#2303/#2304 → #2305, #2215/#2218 → #2214). Dedup only
  matches the same file.
- 8 duplicate an issue or PR a human had already opened (#2262/#2266 → #2253,
  #2182/#2184 → #2181). Dedup only reads issues the bot wrote.
- 4 are about code that only existed in an unmerged revision of the PR
  (#2138, #2178, #2207, #2333). Filing happens per round, before merge.
- 4 had a false premise or described intended behaviour.

## Goal

At most one follow-up issue per merged PR, holding only the deferred findings
that are not already tracked anywhere in the tracker.

## Design

1. **File at merge, not per round.** The panel stops filing. A new workflow,
   `agent-follow-up.yml`, runs on `pull_request_target: closed` (merged only)
   and on `workflow_dispatch`. It reads the PR's newest
   `agent-deferred-findings` check run, written by `github-actions`. That run
   is the last round's deferred set, so findings about code a later round
   removed are gone. The deferred record gains the judge's `reason` and
   `groundedIn` for demoted findings, which the issue body needs.
2. **Dedup against the whole tracker, by root cause.** The mechanical match
   against earlier follow-ups stays. A judge session then compares the rest
   with every open issue (any author) and every closed follow-up, and may mark
   a finding as the same root cause as an earlier finding of the same PR. A
   claimed duplicate counts only when it names an issue from the list it was
   given. If the judge fails, everything is filed, as before.
3. **One issue per PR.** The new findings become one checklist issue titled
   after the PR. Duplicates are listed as "already tracked in #N" in the same
   issue. If every finding is already tracked, nothing is filed. A hidden
   per-PR marker keeps a re-run from filing twice.

Three steps in the job, so the model step never holds the GitHub token:
`collect` (GitHub reads) → `judge` (Claude credentials only) → `file` (issue
write).

## Tasks

- [x] `deferred-findings.mjs`: carry `outOfDiffReason` and `outOfDiffGrounds`
      on demoted records.
- [x] `follow-up-issues.mjs`: select from the deferred check text; multi-record
      parsing; per-PR marker; judge prompt, schema and validation; grouped
      issue rendering; `collect` / `judge` / `file` verbs.
- [x] Tests for each of the above.
- [x] `agent-follow-up.yml`; drop the filing step from `agent-review-panel.yml`.
- [x] Update `docs/design/agent-command-verbs.md`.
- [x] Regenerate `MANIFEST.sha256`; `npm test` and `npm run test:repo` green.
- [x] Self review (one round; see the lessons file); open the PR.

## Known limitations

- Only the last round's record is read. A real defect that sampling noise kept
  out of the final round is not filed.
- The deferred record's `text` is capped at 60000 characters; demoted findings
  past the cap are not filed (the record says how many were omitted).
- When the newest record is not on the PR's last commit, it is used anyway and
  the issue says so. A carried round and a cancelled one look the same here.
- The judge can mark a finding as a duplicate of any open issue, including one
  an outsider wrote to match it. Then nothing is filed, and the match is
  written only to the run summary. Accepted: the cost is a missed follow-up,
  which the per-round filing also risked through its cap.
- The deferred check is trusted by app (`github-actions`), the same posture as
  the merge gate (`CHECK_PRODUCER_APP_SLUG`).

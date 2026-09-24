**Created**: 2026-09-24

# Local harness enforcement

Give this repository the enforcement layers it documents but does not run.
`CLAUDE.md` requires every commit to be `make lint` and test green; today
nothing checks that before CI. Add the missing local gates, then work down a
short list of defects in the agent pipeline that a survey of the same surface
turned up.

## Motivation

A comparison against the sibling repository that this pipeline was ported
from (wafflebase) shows the port took the CI half and left the local half
behind. That repository gates a change four times — at edit time (Claude Code
hooks), at commit time (`pre-commit`), at push time (`pre-push`), and in CI.
This repository has the fourth only, plus a `commit-msg` hook that checks
message shape and nothing else.

The cost is not hypothetical. `docs/design/agent-command-verbs.md` §4b already
lists what is "enforced by nothing" here, and the Apache license header is on
that list: **17 of 486 tracked `.go` files carry no header today**. Nothing
would have caught them, and nothing will catch the eighteenth.

Two further facts shape the scope:

- There is no aggregate verification target. The design doc names `make lint`
  + `go test ./...` as this repository's equivalent of the upstream
  `verify:fast`, but that pair exists only as prose, so a hook has nothing to
  call. This is the prerequisite for everything else.
- Measured on this machine: `make lint` 5.5 s warm, `go test ./...` 35 s cold
  (1705 tests, 81 packages). Both are inside the budget a hook can spend; the
  integration lane (`make test`, needs MongoDB) is not, and stays in CI.

## Approach

Layer the gates by cost, cheapest first, so the expensive one runs least
often:

| Gate | Runs | Cost |
|---|---|---|
| Claude Code hooks | every edit / Bash call | ~0 |
| `pre-commit` | every commit | `make lint`, ~6 s |
| `pre-push` | every push | `make verify`, ~40 s |
| CI | every push | full, incl. integration |

`make verify` is the name the design doc already implies; defining it once
means the hook, the docs and a future agent all call the same thing.

Deliberately out of scope:

- A Go port of the upstream `verify-self` lane runner and its
  `.harness-reports/` artifact. It is the single highest-value missing piece
  (it is what feeds `agent-iterate-ci`'s diagnosis, which today is
  `gh run view --log-failed | tail -c 40000`), and it is far too large to ride
  along with this. It gets its own task.
- The `hunt`, `report-intake`, `spec-to-pr` and `eval` arms. The phase plan in
  `docs/design/agent-command-verbs.md` puts the back half first, and Phase 1
  only started working on 2026-09-22.
- A local runner for the six-lens panel. `.claude/commands/self-review.md`
  states its absence is deliberate.

## Checklist

### Gate 1 — the local enforcement layer

- [ ] `make verify` = `make lint` + `go test ./...`, added to `.PHONY` and to
      `CLAUDE.md`'s command table.
- [ ] `.githooks/pre-commit` runs `make lint`.
- [ ] `.githooks/pre-push` runs `make verify`.
- [ ] `scripts/hooks/session-prime.sh` — SessionStart, non-blocking, states the
      workflow requirements that `CLAUDE.md` may not be read far enough to
      reach.
- [ ] `scripts/hooks/guard-generated-files.sh` — PreToolUse(Edit|Write), exit 2
      on `api/yorkie/v1/**/*.pb.go` and `*.connect.go`, printing `make proto`.
- [ ] `.claude/settings.json` wiring both hooks.

### Gate 2 — the license header, which §4b says nothing enforces

- [ ] Backfill the 17 files. Copyright year from each file's first commit.
- [ ] `scripts/verify-license.mjs`, wired into `make verify` and CI.

### Defects found in the same survey

- [ ] **Inline `@claude <verb>` is silent.** `pull_request_review_comment` is
      subscribed to by `agent-review-reply.yml` alone, whose job requires
      `command == 'reply'`. Typing `@claude review` in an inline thread routes
      to `review`, the job skips, and nothing else listens — no comment, no
      failed check. Add a `help` arm modelled on `agent-implement.yml`'s.
- [ ] **`dependabot.yml` does not cover npm.** `scripts/agent/package-lock.json`
      pins the Agent SDK and carries hand-written `overrides` for `fast-uri`
      and `qs`; nothing updates them.
- [ ] **`ci.yml` installs `@anthropic-ai/claude-code` unversioned** in a job
      holding `contents: write` and `CLAUDE_CODE_OAUTH_TOKEN`. Pin it.
- [ ] **actionlint covers `agent-*.yml` only** while `agent-scripts.yml`
      triggers on all of `.github/workflows/**`. Verify the excluded findings
      are still live, then widen or record why not.
- [ ] **Stale justification.** `agent-review-panel.yml` cites "`ci.yml`'s
      `.harness-reports/` upload" as precedent for `include-hidden-files`.
      `ci.yml` has no upload step; `.harness-reports/` here is a gitignored
      test scratch dir. Correct the comment.

### Rejected after reading the code

- [x] ~~Give `pick-credential.mjs` an `available` output so `@claude fix` pages
      instead of mis-reporting a drained pool as "the branch head did not
      advance".~~ The file's own header refutes it: `pick-fix-credential.mjs`
      works by reading pool state a panel recorded, "these jobs have no panel
      before them and no artifact to read", and liveness cannot be known
      without spending a call. Not a low-cost fix; see the lessons file.

## Review

(pending)

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

- [x] `make verify` = `make lint` + `go test ./...`, added to `.PHONY` and to
      `CLAUDE.md`'s command table.
- [x] `.githooks/pre-commit` runs `make lint`.
- [x] `.githooks/pre-push` runs `make verify`.
- [x] `scripts/hooks/session-prime.sh` — SessionStart, non-blocking, states the
      workflow requirements that `CLAUDE.md` may not be read far enough to
      reach.
- [x] `scripts/hooks/guard-generated-files.sh` — PreToolUse(Edit|Write), exit 2
      on `api/yorkie/v1/**/*.pb.go` and `*.connect.go`, printing `make proto`.
- [x] `.claude/settings.json` wiring both hooks.

### Gate 2 — the license header, which §4b says nothing enforces

- [x] Backfill the 17 files. Copyright year from each file's first commit.
- [x] `scripts/verify-license.mjs`, wired into `make verify` and CI.

### Defects found in the same survey

- [x] **Inline `@claude <verb>` is silent.** `pull_request_review_comment` is
      subscribed to by `agent-review-reply.yml` alone, whose job requires
      `command == 'reply'`. Typing `@claude review` in an inline thread routes
      to `review`, the job skips, and nothing else listens — no comment, no
      failed check. Add a `help` arm modelled on `agent-implement.yml`'s.
- [x] **`dependabot.yml` does not cover npm.** `scripts/agent/package-lock.json`
      pins the Agent SDK and carries hand-written `overrides` for `fast-uri`
      and `qs`; nothing updates them.
- [x] **`ci.yml` installs `@anthropic-ai/claude-code` unversioned** in a job
      holding `contents: write` and `CLAUDE_CODE_OAUTH_TOKEN`. Pin it.
- [x] **actionlint covers `agent-*.yml` only** while `agent-scripts.yml`
      triggers on all of `.github/workflows/**`. Verify the excluded findings
      are still live, then widen or record why not.
- [x] **Stale justification.** `agent-review-panel.yml` cites "`ci.yml`'s
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
- [x] ~~Port `require-ai-disclosure.sh`, the hook upstream pairs with the
      disclosure trailer — `disclosure.mjs` flags its absence itself.~~ It
      would be a permanent no-op. The hook does nothing unless an environment
      variable is set, upstream the local `spec-to-pr` arm sets it, and
      nothing in this repository sets it at all — so the ported hook would be
      present and permanently asleep. The PR-body predicate stays the gate, as
      that header says. (A first draft of this rejection also leaned on
      `claude-code-action` not reading a branch's `.claude/settings.json`.
      That is unverified and `agent-review-panel.yml` strips `.claude/` on the
      opposite assumption; round 2 caught it. The env-var leg was always the
      load-bearing one.)

## Alternative considered for the licence gate

`go install github.com/google/addlicense` in `make tools`, then
`addlicense -check ./...`. One line, no second ecosystem in the local gate, no
skip path at all, and it can *fix* as well as check — which would have made the
17-file backfill a single command.

Not taken, but it is close. Node is already in this repository's workflow
(`verify-doc-links.mjs`, the whole of `scripts/agent/`), the `Docs` workflow was
the right home for an unfiltered check and already had Node, and matching the
grant clause alone is more forgiving than `addlicense`'s template comparison
across a tree with two comment styles and copyright years from 2020 to 2026.

The honest cost of the choice is the skip path: `make verify` cannot run the
licence check without Node, where `addlicense` would have been installed by
`make tools` alongside everything else. That is why `pre-push` refuses outright
rather than inheriting the Makefile's announced skip. If the Node dependency
ever becomes a burden for contributors, `addlicense` is the swap to make.

## Review

Three groups of work, plus the fixes from three review rounds. Commit counts
are deliberately not recorded here: the first version of this section carried
them and they were stale two rounds later, which round 3 caught.

**The local layer (Gate 1).** `make verify`, `pre-commit`, `pre-push`, and two
Claude Code hooks. The split between the two git hooks was chosen from
measurement, not preference — lint 5.5 s, unit tests 35 s — so the per-commit
gate stays cheap enough to keep. Both hooks were proved by breaking them: a
deliberate `lll` violation is refused at commit, and the generated-file guard
was run against nine payloads covering both directions.

**The licence header (Gate 2).** The checker landed after the backfill so no
commit is red in between. It matches the grant clause alone, deliberately: the
tree carries years from 2020 to 2026 and two comment styles, and a stricter
checker would spend its findings on formatting. Eight test cases, including
the one that matters — a mention of the clause past the scan window is not a
header.

**Five defects.** Each verified live before it was touched. The two actionlint
findings were confirmed with the lane's own flags first, fixed separately,
and only then was the scope widened, because the comment being replaced asked
for exactly that order. The inline-help arm ships with two guards, both
confirmed by breaking them — the defect existed because nothing compared the
verb table against the workflows consuming it, so a fix without a guard would
have left the class open.

Two survey items were rejected on reading, above. Both would have passed any
test written for them, which is the argument for reading the target before
trusting a finding about it.

**Three review rounds**, logged in the lessons file: correctness and test
adequacy (1 Critical, 7 Important), design fit and blast radius (3 Important),
security and documentation consistency (2 Important, no Critical). All fixed.
The recurring finding across all three was a claim written but not checked —
four places still denying the licence lane, a replacement justification with no
relative `.go` targets behind it, an assumption about `claude-code-action`
contradicted by this repository's own workflow, and a security note asserting
every action here is SHA-pinned when two of seventeen are. Each is now either
measured or deleted.

Known limitations, deliberate:

- The codecov v3 → v5 move is verified only as far as actionlint's input
  database reaches. Whether the upload succeeds is observable only from a run
  on `main`.
- The pinned Claude CLI version is not auto-updatable — dependabot's npm
  ecosystem reads manifests, and this is an argument in a `run:` step. The
  comment says so rather than leaving the next reader to assume otherwise.
- `pre-commit` lints the working tree, not the index, so a `git add -p` commit
  can pass on code it is not committing. Linting an index-only checkout costs
  a temporary worktree per commit; `pre-push` and CI both see the real tree.
- The largest gap found in the survey is untouched: there is no Go equivalent
  of the upstream `verify-self` lane runner, so `agent-iterate-ci` still
  diagnoses CI failures from `gh run view --log-failed | tail -c 40000`. It
  needs its own task.

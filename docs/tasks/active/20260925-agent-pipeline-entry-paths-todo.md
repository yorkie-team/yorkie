# Agent pipeline: the entry paths a human PR uses

**Created**: 2026-09-25

Five defects that share one shape: the `@claude` pipeline was built around
PRs the pipeline itself opened, and a PR a human opened is treated as half a
citizen by it. Plus two defects the fix arm records and does not fix.

Everything here lives under `.github/`, which no credential in the pipeline
may write — so this lands by hand.

## 1. `agent-task.yml`, and who applies `agent:candidate`

**Verified first.** `agent:candidate` is READ by two workflows, not three:

| File | Role |
| --- | --- |
| `agent-review-panel.yml` (~L515) | trusts a `Fixes #N` issue as the design-fit spec only if labelled AND non-Bot |
| `agent-review-on-demand.yml` (~L340) | the same rule, for `@claude review` |
| `agent-implement.yml` (L806) | the only WRITER — `gh issue edit --add-label`, after it opens its PR |
| `set-state.mjs`, `mark-ready.mjs` | preserve it across lifecycle relabelling (not readers of its meaning) |

So a human-opened PR that says `Fixes #N` gets `design-fit` — a **blocking**
lens whose rubric is written against the issue's `outcome` + `acceptance`
criteria — reviewing with `/tmp/issue.txt` empty.

- [ ] Add `.github/ISSUE_TEMPLATE/agent-task.yml`, a structured form whose
      fields are the ones `lenses/design-fit.md` names: outcome, acceptance
      criteria, non-goals, plus context and pointers.
- [ ] **Do NOT auto-apply `agent:candidate` from the form.** Reasoning in the
      form's own comment and below.
- [ ] Make the label reach human PRs: `agent-loop.yml` labels the issue named
      by the PR body's `Fixes #N` when a maintainer opts the PR in.

### Why the form must not auto-label

`agent:candidate` is not a category tag; it is the panel's **provenance
check**. It answers "did a human with triage rights vouch for this issue as a
spec?" — which is why the panel pairs it with a non-Bot author check and
otherwise reviews with no spec at all.

An issue form applies its `labels:` to whatever any GitHub user submits. This
repository is public. Auto-labelling would let an arbitrary account write the
text a blocking lens reviews somebody else's PR against — reachable by filing
an issue and writing `Fixes #N` in a PR body. The label currently costs an
attacker triage permission; auto-labelling would cost them nothing.

So the form carries no labels, and the label is applied by an actor that has
already been permission-checked: `agent-implement.yml`'s `finish` job (which
runs only behind a write-access gate), or `agent-loop.yml` (same gate).

## 2. Documentation-only PRs sit outside the loop — DECIDED, implemented

`ci.yml` carried a workflow-level `paths-ignore`. A PR confined to those paths
creates **no CI run**, so there is no `workflow_run` and neither
`agent-review-panel.yml` nor `agent-iterate-ci.yml` ever fires. `@claude loop`
labels such a PR and nothing happens — the workflow's own confirmation comment
said so.

### Options weighed

| Option | Cost | Verdict |
| --- | --- | --- |
| Remove `paths-ignore` outright | every docs PR runs golangci-lint, buf, `make build`, docker compose + `-race` integration tests (~13.5 min, a MongoDB) | correct, wasteful |
| A cheap always-running job in a SECOND workflow | the panel's gate asserts `workflow_run.path == '.github/workflows/ci.yml'`, deliberately, because `workflows: ["CI"]` matches a DISPLAY NAME. A second producer means widening that gate | rejected — it trades a security property for a cost saving |
| **Move the filter from the workflow to the `build` job** | one `ci-target-check` run per docs PR: a checkout and two `dorny/paths-filter` evaluations, ~20s | **chosen** |
| Defer | leaves `@claude loop` promising something it cannot do | rejected; the tradeoff is not unclear |

### Why the job-level filter is the cheapest correct option

- The run is **created**, so `workflow_run` fires and the panel/iterate-ci
  triggers work without any change to their gates.
- `build` still does not run on a docs-only PR, so no Go lane, no MongoDB and
  no `-race` suite is spent. The *substance* of every "a docs-only PR runs
  none of the lanes" claim in the tree survives; only the mechanism changes
  from "no run" to "run, build skipped".
- It fixes a second thing for free. GitHub's documented behaviour: a workflow
  skipped by **path filtering** leaves its required checks Pending forever,
  while a job skipped by a **conditional** reports Success. Today, making
  `build` a required check would deadlock every docs PR; after this it does
  not.
- `mark-ready.mjs`'s "no CI run for this sha → promote it by hand" branch
  becomes unreachable, and a docs-only managed PR can now be promoted by the
  gate instead of by hand. That IS item 2's goal.

### The filter's fail direction

`predicate-quantifier: every` over `['**', '!<each ignored path>']` means: a
file counts if it matches `**` AND none of the negations; the filter is true
if **at least one** changed file counts. So an unmatched path — a brand-new
directory, a new extension — counts, and `build` runs. The only way to lose
CI on code is for a negation to match a code path, and every negation is a
docs extension or a docs directory.

- [ ] `ci.yml`: drop `pull_request.paths-ignore`; add a second
      `dorny/paths-filter` step with `predicate-quantifier: every`
- [ ] Point `build`'s `if:` at it
- [ ] Update every comment in the tree that states the old mechanism:
      `ci.yml`, `docs.yml`, `agent-scripts.yml`, `agent-loop.yml`'s `ciNote`,
      `mark-ready.mjs`, `review-panel.mjs`'s `MECHANICAL_COVERAGE_NOTE`,
      `checks.test.mjs`, and §2/§4b of the design doc
- [ ] New guard in `checks.test.mjs`: no workflow-level `paths-ignore` on
      `ci.yml`'s `pull_request`, and the build filter is `**` plus negations
      only, under `every`

## 3. The unreachable carve-out in `agent-fix.yml`

**Verified.** The `ci-clear` gate's "no CI run at all → CLEAR" branch is
justified as keeping `@claude fix` usable on docs-only PRs. It cannot be
reached that way:

- the step carries `if: steps.eligible.outputs.eligible == 'true'`;
- `fix-eligible.mjs` returns `eligible: false` when `total === 0`, i.e. when
  the head carries no `agent-review-<lens>` check runs;
- only the panel writes those (`agent-review-on-demand.yml` holds
  `checks: read`, never write — the script's own comment says so);
- the panel runs only from a CI `workflow_run`.

No CI run → no panel → no lens checks → refused at `fix-eligible`, before the
carve-out is evaluated. Dead code, every time.

Item 2 removes the reason it was written: a docs-only PR now has a CI run.
What is left in the "no run" state is "we cannot see CI's conclusion for this
sha" (a deleted run, or one past its retention while the check runs survive) —
and the gate's own stated rule is *fails toward NOT clear on anything
unreadable*.

- [ ] Flip the branch to refuse, with a comment recording that the docs-only
      justification was false and what replaced it
- [ ] Widen the refusal message to cover "no CI run is visible"
- [ ] Invert the guard in `checks.test.mjs` that pinned the old answer

## 4. A 90-minute wall against a 60-minute token

`agent-fix.yml`'s `fix` and `agent-review-panel.yml`'s `fix` both set
`timeout-minutes: 90`; both carry the same ⚠️ comment saying an installation
token lives one hour, so a round past the hour loses the credential in
`.git/config` and the push 401s. `agent-implement.yml`'s `implement` job is a
third instance of the same defect (recorded in the design doc's Phase I
residuals).

### Why "the wall is not shortened to 55" was wrong

The existing comment argues that 55 "would refuse exactly the rounds the
reasoning says to allow". The rounds it protects are the ones between minute
60 and minute 90 — and those rounds **cannot push**. Their credential is
already dead. They spend model budget and a round from `MAX_REVIEW_ROUNDS`
and produce nothing, which is precisely the outcome the raise from 45 to 90
was made to avoid. There is nothing there to allow.

Refreshing instead is not available: `create-github-app-token` cannot extend a
token, minting a second one needs a step, and `checks.test.mjs` enforces that
**nothing runs after the agent in its own job** — that rule is load-bearing
for the whole trust model and is not worth trading for 30 unusable minutes.
The change that would genuinely buy them is the agent handing a bundle to a
trusted job that pushes; the design doc already names it as the next change,
and it is a different piece of work.

- [ ] `timeout-minutes: 55` on all three jobs, with the arithmetic written
      down: the token is minted at or after job start, so a wall at 55 keeps
      the whole round inside the credential's hour with ≥5 minutes of margin
      for the final push, its `git ls-remote` confirmation and clock skew
- [ ] Tell the agents their budget in the prompt (a convention, and labelled
      as one)
- [ ] The panel's cancelled-cause page states the number twice —
      `checks.test.mjs` pins page wording == wall == `agent-fix.yml`'s wall
- [ ] Add an assertion that the wall is **under 60** and say why

## 5. The fixer prompts do not call `make verify`

`docs/design/agent-command-verbs.md` §4c records this. Switching is not a pure
rename: `make verify` = `make lint` + `make verify-license` + `go test ./...`.

**Checked, as the brief asks:** `make verify-license` announces SKIPPED when
node is absent — but all three fixer jobs run `actions/setup-node@v4` with
`node-version: 22.x` as an **ungated first step** (`agent-fix.yml` L132,
`agent-iterate-ci.yml` L129, `agent-review-panel.yml` L1752). Node is present,
so the licence gate really runs. The switch closes a real hole rather than
adding a line that skips.

- [ ] `make lint` + `go test ./...` → `make verify` in all three prompts
- [ ] `agent-iterate-ci.yml`'s no-log fallback text too
- [ ] Drop the Makefile's "the prompts still spell it out" note
- [ ] Rewrite §4c to describe what landed

## Verification

- [ ] `make verify`
- [ ] `node --test 'scripts/test/**/*.test.mjs'`
- [ ] `cd scripts/agent && npm ci --no-audit --no-fund --ignore-scripts && npm test`
- [ ] `docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:1.7.12 -color -shellcheck= -pyflakes=`
- [ ] Break each changed gate, confirm its guard fails, restore — recorded in
      the lessons file

## Review

(filled in on completion)

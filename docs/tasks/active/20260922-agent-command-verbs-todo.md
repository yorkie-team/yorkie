# Install the advisory half of the `@claude` command surface

**Created**: 2026-09-22

Design: `docs/design/agent-command-verbs.md`. That document decides which verbs
land in which order and why; this one is the execution record for Phase 0 and
Phase 1.

## Scope

Phase 1 only: `@claude review` and `@claude summarize`, both advisory, both
read-only toward code. No gating check runs, no bot pushes, no GitHub App. The
kill switch (`AGENT_PIPELINE_ENABLED`) stays unset, so nothing runs until a
maintainer turns it on deliberately.

## Phase 1 — done in this branch

- [x] Port the module closure of the two workflows' entry points into
      `scripts/agent/` (26 modules + 22 `node:test` suites, own lockfile,
      outside the Go build).
- [x] Drop `fix-brief.mjs` from the Phase 1 batch: neither advisory workflow
      invokes it. (Phase 2 brings it back — the panel's fix job does.)
- [x] `workflow-presence.mjs` — skip, rather than delete, the four guards whose
      workflow arrives in Phase 2 or 3. They re-arm by themselves when it lands.
- [x] Rewrite `CLASS_RULES` for the Go layout: `.golangci.yml`, the `Makefile`
      and the buf configs are `policy` (they decide what the lanes check);
      `docs/design/**` is `design-spec`; `_test.go` stays `code`; generated
      `.pb.go` falls through to `code` rather than being demoted.
- [x] Rewrite `MECHANICAL_COVERAGE_NOTE` from this repository's real lanes, and
      rewrite the test that pins each claim. Both halves were re-derived, not
      translated — the upstream assertions described a pnpm monorepo and would
      have passed here while describing a repository that does not exist.
- [x] Scope each lens's `appliesWhen` to the Go tree.
- [x] `PERMITTED_MCP_TOOLS` is now empty: the entries it carried were the
      hunters' tools, and no hunter is ported.
- [x] Redaction drops the upstream API-key rule — a yorkie project key is a bare
      `shortuuid`, with no shape to match that would not also redact every sha.
- [x] Port `agent-review-on-demand.yml` and `agent-summarize.yml`, swapping the
      GitHub App token for `GITHUB_TOKEN` and excluding this repository's
      generated protobuf from the diff body (never from the changed-file list).
- [x] `agent-scripts.yml` — the test lane, triggered by `scripts/agent/**` and
      by any workflow change, because several guards read `.github/workflows/`.
- [x] Document the two verbs in `CONTRIBUTING.md`, in one table.

Counts move as phases land; run `npm test` in `scripts/agent` for the current
number. With phases 2 and 3 installed the previously-skipped guards run, because
their workflows now exist.

## Phase 1 — still open

- [ ] **Enable it.** Set the `AGENT_PIPELINE_ENABLED` repository variable to
      `true`. Until then every workflow's first condition is false and nothing
      runs, which is the intended state for the merge itself.
- [ ] **Register the token pool.** `pick-credential.mjs` is ported and every
      model-running job is wired to it, reading `CLAUDE_CODE_OAUTH_TOKEN_1..8`.
      None of those secrets exist yet, so every job falls back to the single
      ambient credential and six concurrent lenses share it. Register slots if
      the first real rounds rate-limit.
- [ ] **The first fork PR.** Confirm the placeholder comment posts under
      `GITHUB_TOKEN`. The design doc records why it is expected to and what to
      do if it does not; the placeholder is `continue-on-error` either way.
- [ ] **The Phase 1 exit criteria.** Run `@claude review` on twenty PRs and
      compare its blocking findings against CodeRabbit's on the same diff.
      Phase 2 is justified only by that comparison.

## Phase 0 — done in this branch

- [x] `.claude/commands/self-review.md` — the bounded loop, and the rule that a
      skipped review never reads as a clean round.
- [x] `CLAUDE.md` step 3 points at it instead of carrying one prose line.

It runs the harness's own reviewer, not the six lenses: there is no local runner
for the panel, so the command says which reviewer it ran and refuses to report a
round as a panel round. That asymmetry is deliberate — a local runner would need
its own credential handling to say anything the cloud panel does not.

## Phase 2 — done in this branch

- [x] The remaining module closure: round guard + latch, promotion gates,
      credential pickers, scope resolver.
- [x] `agent-review-panel.yml`, `agent-loop.yml`, `agent-rerun.yml`.
- [x] `CI_DEFINING_PATHS` rewritten — the inherited list named a pnpm
      workspace's manifests and would have refused nothing here.
- [x] The fixer verifies with `make lint` + `go test ./...`; the integration
      lane is left to the CI run the push starts.
- [x] The linter is installed from a version pinned in the workflow, never from
      the branch's `make tools`, with a test asserting it.

## Phase 3 — done in this branch

- [x] `agent-fix.yml` and `agent-review-reply.yml`.
- [x] Same untrusted-setup rule as the panel's fix job.

## Phase 2 and 3 — still open

- [ ] **The GitHub App** (`AGENT_APP_ID`, `AGENT_APP_PRIVATE_KEY`) and the
      `agent` environment. Without them the panel still reviews, records check
      runs and latches, but never dispatches a fixer — a `GITHUB_TOKEN`-authored
      push does not re-trigger workflows, so the loop would stop silently.
- [ ] **Branch protection.** Six `agent-review-*` check runs appear on a
      labelled PR. Whether any is *required* to merge is a repository setting
      and should start as "no".
- [ ] **Keep the linter pin in sync** with the Makefile's `tools` target. The
      pin going stale shows up as a lint disagreement, which is the direction
      to fail in, but it still has to be noticed.

## Review rounds

Two `/code-review high` passes over the branch, thirty findings, all applied or
recorded. The classes worth carrying forward:

- **Silent stalls** — red CI, a panel hitting its own wall, a CI run concluding
  `cancelled`/`timed_out`, two never-cleared throttle markers, and the
  no-credential pager marked `continue-on-error`. Fixed, and the `stalled` net
  now lists the `cancelled` shapes it was missing.
- **Token scope** — two App-token mints ported without `permission-*` narrowing
  (one also on a mutable tag), in the two jobs that run an agent beside the
  token. Both now match the four that were already narrow.
- **Trust** — the reply arm derived "agent branch?" from the branch name alone
  with no same-repo check, and the rerun arm deleted any comment carrying the
  paged marker with no author check.

## Still worth doing, not done here

- [x] **A test for the permission list.** Added: every
      `create-github-app-token` mint must be SHA-pinned, must narrow at all, and
      must not grant `workflows`. Mutation-checked — unpinning, granting
      `workflows`, and removing the narrowing each red it.
- [ ] **The on-demand throttle is still racy.** Two `@claude review` comments
      seconds apart can both pass the gate before either marker lands. The
      release valve added here fixes the stuck case, not the double-run one.
- [ ] **A 60-minute credential in a 90-minute job.** An App installation token
      lives exactly one hour and cannot be extended, so a fix round that runs
      past it loses the credential in `.git/config` and in `github_token` — the
      push 401s and the steps that would report that 401 too, stranding the
      placeholder comment. Both fixer jobs carry a comment saying so. Closing it
      means re-minting before the push and rewriting the git credential, which
      wants the App configured and a real round to test against.
- [ ] **The CI arm checks out a moving branch.** `agent-iterate-ci.yml` checks
      out `workflow_run.head_branch`, not the head SHA, and has no re-check of
      the kind `agent-fix.yml` does — so a push landing mid-run has the fixer
      apply commit A's failure log to commit B. Bounded by the attempts counter,
      but it wastes a round and the diagnosis is wrong.

## Inherited defects, recorded not fixed

The fourth review round found fifteen more, and eleven are in the vendored
pipeline rather than in the adaptation — real, reproduced, and upstream's. They
are listed here rather than fixed, for the reason the vendoring is split across
two commits in the first place: surgery inside a module this repository did not
write is what makes the next sync from upstream expensive, and none of these can
fire until the surface is enabled. Worth carrying upstream.

- `review-surface.mjs` demotes a finding whose blamed commit is an ancestor of
  the freeze point, so a real defect in code merged from `main` after the freeze
  stops gating. Fail-open.
- `novelty.mjs`'s content probe accepts a whole-tree `git grep -F` hit as proof a
  line predates the change, so newly added boilerplate drops its finding.
- `review-round-guard.mjs` back-fills `output.text` only when absent, never when
  the list response TRUNCATED it — so the convergence page never fires on exactly
  the finding-heavy PRs it exists for. The module has no test file.
- `fix-brief.mjs`'s "a brief we could not build is not an empty brief" guard
  tests the lens count rather than whether any finding parsed, so an
  infra-failed round dispatches the fixer with an empty work list.
- `agent-iterate-ci.yml`'s no-commit page has no supersede guard inside a
  `cancel-in-progress` group, so a superseded run can latch a PR that was only
  superseded.
- `redact.mjs`: the echo-frame layer requires a literal `value:`, which undici's
  wording omits — a 16–23 character unprefixed credential then survives every
  layer. Separately, layer 4's separator class accepts a bare space, so ordinary
  prose ("OAuth token rejected", "the secret material") is mangled.
- `panel-round-comment.mjs` renders "all N lenses passed" using the full lens
  count while excluding non-gating ones from `blocked`, so an errored advisory
  lens reads as green beside a red check run.
- `fix-report.mjs` / `rebuttal.mjs` recompute `tied` per claim instead of making
  it sticky, so a third equal-scoring claim clears a genuine ambiguity.
- `metrics.mjs` carries embedded records forward with no size cap, so one
  duplicated data block can push the summary past GitHub's comment limit and the
  bail fires before the cleanup that would fix it.
- `ai-prompt.mjs`'s `isFixable` filters on lane and severity only, so synthesised
  infra records ("Review could not run") reach the copy-paste fix prompt.
- `set-state.mjs`'s label write is a read-then-PUT-whole-set, so two arms writing
  concurrently revert each other and delete any label a human added in between.

## Not in scope

`@claude fix` on an **issue** (issue → PR, `agent-implement.yml`), the CI
iteration workflow, the hunters, the debug reporter, and the eval rig. See the
design document's Non-Goals.

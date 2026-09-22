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
- [x] Drop `fix-brief.mjs`: nothing imports it and neither workflow invokes it.
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

Local: `npm test` in `scripts/agent` — 701 tests, 693 pass, 8 skipped (the
Phase-2 guards), 0 fail. `node scripts/verify-doc-links.mjs` green.

## Phase 1 — still open

- [ ] **Enable it.** Set the `AGENT_PIPELINE_ENABLED` repository variable to
      `true`. Until then every workflow's first condition is false and nothing
      runs, which is the intended state for the merge itself.
- [ ] **A token pool.** Six lenses run concurrently against one
      `CLAUDE_CODE_OAUTH_TOKEN` today. Add `CLAUDE_CODE_OAUTH_TOKEN_<n>` if the
      first real runs rate-limit; `pick-credential.mjs` upstream is the selector
      to port if so.
- [ ] **The first fork PR.** Confirm the placeholder comment posts under
      `GITHUB_TOKEN`. The design doc records why it is expected to and what to
      do if it does not; the placeholder is `continue-on-error` either way.
- [ ] **The Phase 1 exit criteria.** Run `@claude review` on twenty PRs and
      compare its blocking findings against CodeRabbit's on the same diff.
      Phase 2 is justified only by that comparison.

## Phase 0 — not in this branch

`/self-review` (a `.claude/commands/` definition) is Phase 0 in the design and
is deliberately not here: it is a local command with no CI surface, and mixing
it into a branch that is otherwise a workflow-and-scripts port would make both
harder to review. It needs no infrastructure, so it can land any time.

## Not in scope

`@claude loop`, `@claude rerun`, `@claude fix`, the bare-mention `reply`
fallback, and `@claude fix` on an issue. See the design document's phases 2, 3
and its Non-Goals.

# Keep the agent loop inside the PR's scope

**Created**: 2026-10-05

The October 2026 loop PRs (#2081, #2084, #2098, #2100, #2108, #2111, #2112,
yorkie-js-sdk#1442) mostly paged for one reason: a confirmed finding on code the
PR never touched, disputed, upheld, repeated. When the fixer acted on one
instead, it wrote the fix into the PR and the next round reviewed it. A few
pages had operational causes: a cancelled run nothing replaced, a usage window
nobody retried, a CI lane with no wall.

Design: `docs/design/agent-command-verbs.md` §7.

## Plan

- [x] **Out-of-diff findings never block (P1, P3).** `out-of-diff.mjs`:
      the anchor test with git (file untouched, or ≥10 lines from every
      changed line), then a causation judge holding the PR diff (revert test,
      grounded `independent` only). Routed to `backlog` in `routeFinding`,
      carried findings judged at file level or inheriting their twin,
      a section in the check body, fields in the deferred record,
      `follow-up-issues.mjs` filing `agent:follow-up` issues, and
      `exhaustedFindings` filtering `backlog` so the guard cannot page on one.
- [x] **CI walls (P7).** The bench lane already had 60 minutes (#2108); give
      complex-test and load-test 30.
- [x] **Stuck reviewing and usage windows (P5, P6).** `agent-sweep.yml` +
      `loop-sweep.mjs`: re-run CI once for a PR idle in `agent:reviewing`
      with nothing running, then page; retry usage-window pages (now marked by
      `fix-outcome.mjs`, the round guard and the no-live-credential page) on a
      60/180/360-minute backoff, three times per head.
- [x] **Fixer scope and lint (P2, P4).** `fix-guard.mjs`: a pre-push hook in
      the fixer's checkout (guide) and a check in each report job (gate) that
      pages when a round touches a file outside the original diff plus tests
      and docs, or adds reviewer-directed text; warns on weakened assertions
      and Go-only CRDT rule changes. All three fixers, prompts updated.
- [x] Tests for every new decision; `npm test` green on Node 22; actionlint
      clean.
- [x] Design doc §7 and this pair.

## Out of scope

- Refunding fix rounds spent by an infra failure that the sweep then retries.
  Only a maintainer's `@claude rerun` grants budget; a bot that could would be
  the party the bound constrains choosing its own bound.
- Stripping out-of-scope fixer commits automatically. Needs the trusted push
  path the design doc lists as future work.
- Reading yorkie-js-sdk to check CRDT parity. The lint can only ask.

**Created**: 2026-09-26

# Harden the advisory verbs

Back-port three fixes from yorkie-js-sdk (#1389, #1390) to the `@claude
review` / `@claude summarize` workflows and `scripts/agent/`. js-sdk vendored
these files from yorkie 33810d95, so the same defects are live here.

## Problems

1. **`@claude summarize` has never run.** `claude-code-action` was called
   without `github_token`, so it tried an OIDC exchange and failed before the
   model started. Given a token, it then runs `git fetch origin main` to
   restore `.claude/` from the default branch, which fails with no checkout.
2. **Any GitHub account could start a model run.** Both advisory verbs
   accepted the PR author as a trigger. On a public repository that is anyone:
   a fork PR plus one comment ran a model unattended over attacker-chosen text,
   in a process whose environment holds the Claude OAuth token.
3. **A model session could read its own credential.** A bare `Read` rule
   reaches any absolute path, including `/proc/self/environ`, and the verdict
   is published. `redactSecrets` masks the literal value, not an encoding.

## Plan

- [x] summarize: model job gets `contents: read`, `pull-requests: read` and
      `github_token: ${{ github.token }}`; shallow, credential-free checkout of
      the default branch before the action; `/tmp/summary.md` uploaded as an
      artifact; a new `publish` job (no model, `pull-requests: write`,
      `issues: write`) downloads it and edits the running comment when
      `summarize` succeeded or failed. `release-throttle` still covers
      cancelled and skipped.
- [x] summarize: deny `Read(//proc/**)` and `Read(//sys/**)` in `claude_args`.
- [x] review and summarize gates: drop the PR-author path; require `admin`,
      `maintain` or `write`.
- [x] `scripts/agent/ask.mjs`: `DENIED_READ_PATHS`, `deniedReadRules()`, and
      `disallowedTools` in `buildSessionOptions`, with a test.
- [x] Correct stale comments and docs: workflow headers, the
      `pick-credential.test.mjs` exception reason (it claimed summarize has no
      checkout), `CONTRIBUTING.md` verb table, and
      `docs/design/agent-command-verbs.md`.

## Verification

- [x] `cd scripts/agent && npm test` — 925 pass, 0 fail, including the new
      `/proc`/`/sys` test.
- [x] `node --test scripts/test/*.test.mjs` — 152 pass.
- [x] actionlint 1.7.12 over `.github/workflows/*.yml` — clean.
- [x] `make verify` — no Go touched; run to confirm the gate is unaffected.
- [ ] On GitHub after merge: `@claude summarize` on a PR posts a summary, and
      the same comment from an account without write access is ignored.

## Review

The workflow and `ask.mjs` diffs applied to yorkie's copies unchanged, since
the files had not drifted from the vendored version. Differences from js-sdk
are wording only: comments that said "upstream" or "on yorkie" were rewritten
for this repository, the summarize header now lists every posting job, the
summarize step name no longer claims "no repo credentials", and the
pick-credential exception reason no longer claims there is no checkout
(js-sdk left both stale).

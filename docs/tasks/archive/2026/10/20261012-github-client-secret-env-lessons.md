# Lessons: Keep the GitHub OAuth client secret off the command line

**Created**: 2026-10-12

## Notes

- The issue (#2177) was filed against `39a4bea`, before #2176 merged. Two of
  the three secrets it names — the MongoDB URI and the cluster secret — were
  already moved to environment variables by the time #2176 landed on `main`,
  so the only live part of the finding was
  `--auth-github-client-secret`. Checking the current tree before implementing
  a follow-up filed against an older commit is worth the one `git show`.
- The chart's Secret is `templates/yorkie/backend-secret-key.yaml`, named after
  the first credential it carried rather than after its contents
  (`<yorkie.name>-credentials`). The GitHub client secret is a fourth key in
  the same Secret; no new Secret, so no new name for an operator to collide
  with via `secretKeyExistingSecret`.
- `values.yaml` never declared `githubClientSecret` (nor any of the other
  `github*` args) — the template reads them straight off `.Values.yorkie.args`
  and operators set them with `--set`. The key is added to `values.yaml` now so
  the Secret's contents are discoverable, which also means an empty default,
  matching the `if` guard that already existed in the template.

## Review rounds

- Not run: this branch was produced by a one-shot autonomous run, which is not
  granted the reviewer subagent. Review is CI, `@claude review` and a human on
  the PR.

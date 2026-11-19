**Created**: 2026-11-19

# Lessons: move the analytics chart's init script out of configmap.yaml

## Notes

- The fix the issue names first — scoping the `!build/charts/**/*.yaml`
  negation in `.github/workflows/ci.yml` — was unavailable: this run is
  forbidden from touching `.github/workflows/**`, and the workflow that
  hands the branch over refuses a branch that does. The issue's second
  option (move the script out of a `.yaml` file) was taken instead.
- Equivalence was checked by rendering the chart at `HEAD~` and at the tip
  with `helm template` and diffing the ConfigMap, rather than by eye: the
  payload has significant trailing whitespace on a few lines and the
  provisioning test matches on line continuations, so a hand-retyped
  extraction would have been a silent behaviour change.
- The reason the extracted files cannot drift back into YAML is now pinned
  in the test itself, with the `ci.yml` mechanism written down next to the
  assertion. A comment in `configmap.yaml` alone would not have survived,
  since nothing reads it.

## Self review

Not run: this run is explicitly instructed not to dispatch `/self-review`,
and it is granted no tool that can launch a reviewer subagent. The PR is
opened as a draft for CI, `@claude review` and a human to review.

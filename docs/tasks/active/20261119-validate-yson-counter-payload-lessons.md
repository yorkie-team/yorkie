**Created**: 2026-11-19

# Lessons: validate a Go-built yson.Counter payload

## Notes

- The reviewer's suggested fix ("validate in `setNewCounter`/`AddNewCounter`")
  would not change behavior: those already `panic` on the `RestoreHLL`
  error with the same `ValidateHLLPayload` message. The actionable half of
  the finding is the *entry point* — `client.WithInitialRoot` — which takes
  caller-supplied YSON values and has an error return available, and
  `Counter.Marshal`, which must refuse what `Unmarshal` refuses.
- Validation is placed in `Attach` right after the options are folded and
  before `attachDocument`, so a malformed root fails before any RPC rather
  than in step 05, which runs after the server already holds the attachment.

## Self review

Not run: this branch was produced by the autonomous one-shot agent, which is
granted no tool that can launch the reviewer subagent. CI, `@claude review`
and a human reviewer are the gates.

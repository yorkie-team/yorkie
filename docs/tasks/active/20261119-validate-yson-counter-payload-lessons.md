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

- `TestClient`'s `x-shard-key` subtest installs an mpatch on
  `UnimplementedYorkieServiceHandler.ActivateClient` and never unpatches it,
  so a second test that patches the same method makes one of them fail
  inside the other's handler — a panic in a goroutine after the test
  completed. The new client test serves its own handler type instead.
- Validate accepts an empty-but-non-nil register slice. `setNewCounter`
  skips the restore for `len == 0` and `parseDedupCounter` normalizes `""`
  to nil, so rejecting it would have made `Marshal` fail on a value that
  was previously fine and is harmless on the apply path.

## Verification

`make lint`, `make verify-license`, `make verify-modernize` and
`go test ./...` are green. `make test` (integration, needs MongoDB) was not
run here; CI covers it.

## Self review

Not run: this branch was produced by the autonomous one-shot agent, which is
granted no tool that can launch the reviewer subagent. CI, `@claude review`
and a human reviewer are the gates.

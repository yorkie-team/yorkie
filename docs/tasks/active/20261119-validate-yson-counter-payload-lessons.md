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

- The register payload was the narrowest of the apply path's panics, and a
  guard that stops there reads as covering more than it does. The ways
  `SetYSONElement`/`AddYSON` panic were enumerated instead:
  `crdt.NewCounter` on a value its type cannot hold or an unknown type,
  `RestoreHLL` on wrong-length registers *for any counter type* (it creates
  the HLL when there is none, so a normal counter is not spared),
  `buildDescendants` on an empty tree text node, `index.Node.Append` on a
  text-typed tree root with children, and the default branch on a value of
  a type it does not carry (`int` among them). `yson.ValidateElement`
  mirrors that list, and a table test asserts each rejected value really
  does panic on the apply path — the two drift apart otherwise.
- A pre-RPC guard must be no stricter than the step it protects. Step 05
  skips a key the document already holds, so validating the whole root
  would refuse an attach over a value never read. `Attach` runs the same
  `Get(k) != nil` test, and step 05 re-validates what it does set: a key
  held at attach time can be deleted by a change in the pack the attach
  applies, which would otherwise reach `SetYSONElement` unchecked.

## Verification

`make lint`, `make verify-license`, `make verify-modernize` and
`go test ./...` are green. `make test` (integration, needs MongoDB) was not
run here; CI covers it.

## Self review

Not run: this branch was produced by the autonomous one-shot agent, which is
granted no tool that can launch the reviewer subagent. CI, `@claude review`
and a human reviewer are the gates.

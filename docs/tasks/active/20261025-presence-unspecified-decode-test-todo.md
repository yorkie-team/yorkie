**Created**: 2026-10-25

# Pin how a reader decodes CHANGE_TYPE_UNSPECIFIED presence

Issue: #2230

## Problem

`api/yorkie/v1/capabilities.go:39-47` documents the rollout hazard of
`presence-patch`: a server that predates the capability stores a patch with an
empty change type and serves it back as `CHANGE_TYPE_UNSPECIFIED`, "which
readers fail to decode".

Nothing in the suite covers that value. The only presence-change-type test,
`api/converter/converter_test.go:349`, feeds `PresenceChange_ChangeType(99)` —
an enum value absent from `PresenceChange_ChangeType_name` — and so exercises
only the `ErrUnsupportedPresenceChangeType` branch that `from_pb.go:439` guards
with that map lookup. It passes no matter what `CHANGE_TYPE_UNSPECIFIED` does.

And `CHANGE_TYPE_UNSPECIFIED` does something different from what the comment
suggests: `FromPresenceChange` returns a zero `presence.Change` and a nil
error, and `inner.Change.Execute` sends a zero change down its `default` arm,
which stores the change's nil presence under the actor. On the Go side the
stale patch therefore wipes the sender's presence silently rather than being
rejected; it is the JS SDK that throws `ErrInvalidType` and stops its sync
loop, as `docs/design/presence-patch.md:55-63` describes. Both outcomes are
breakage, but only one of them is what the comment says, and neither is
pinned by a test.

## Scope

Tests for the behaviour that is actually there, plus a comment that matches
it. No behaviour change.

Decoding `CHANGE_TYPE_UNSPECIFIED` into an error is deliberately *not* done
here: `from_pb.go:433-442` keeps the defined-but-unused types decoding as
before because stored rows go through the same function, so erroring on them
would make a document that a pre-capability server already wrote unreadable
rather than merely lossy. That trade-off is the existing design; this task
documents and pins it, and flags the wipe as a known consequence.

## Steps

- [x] `api/converter/converter_test.go`: add a case for
      `CHANGE_TYPE_UNSPECIFIED` (both the named constant and the zero-value
      `&api.PresenceChange{}` a pre-capability server emits) asserting the
      decode succeeds into a zero `presence.Change`, and one for
      `CHANGE_TYPE_DELETE`, the other defined-but-unused type that takes the
      same path. Name the existing enum-99 case for what it covers: an
      *undefined* type.
- [x] `pkg/document/wire_forward_compat_test.go`: pin the whole rollout path
      end to end — a change whose type this build does not produce encodes as
      `CHANGE_TYPE_UNSPECIFIED` (`to_pb.go:210`), decodes back to a zero
      change, and executing that change replaces the actor's stored presence
      with nil. That last assertion is the data-affecting consequence the
      capability comment exists to prevent.
- [x] `api/yorkie/v1/capabilities.go`: say which reader fails and how, instead
      of "readers fail to decode".
- [x] `make lint` and targeted `go test ./api/converter/... ./pkg/document/...`.

## Notes

- `presence.Change` has no exported constructor for an empty change type, and
  none is added: the tests build the struct literally, which is also how a
  decoded stored row arrives.
- No architecture change, so no new `docs/design/` topic. The design doc
  already states the JS-reader behaviour correctly and is left alone.

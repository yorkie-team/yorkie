**Created**: 2026-04-10

# Lessons: merged* fields snapshot encoding

## "Runtime-only" is a claim, not a property

The original concurrent-merge-split fixes added four fields to
`TreeNode` and labeled them "runtime-only". Nobody asked what that
meant for snapshot roundtrip. The answer turned out to be: silent data
loss for any client that attaches between a remote merge and a
concurrent insert into the merged-away parent.

Rule: when adding state to a CRDT node that is consulted during
concurrent op application, write a snapshot-roundtrip test BEFORE
labeling the field runtime-only. "Runtime-only" must prove it survives
the scenarios that actually read it, not just the single-session
scenarios tested in integration.

## The diagnostic test was 10x cheaper at the right layer

The first attempt was an integration test with three clients and a
forced server snapshot — which required the `amd64` monkey-patch
(unusable on arm64 Macs) and a lot of timing coordination. Reframing
the same question at `api/converter` level (two `Document` instances
exchanging change packs, plus one `SnapshotToBytes` call) collapsed
the repro into ~60 lines, ran in milliseconds, and isolated the
divergence to the snapshot encoder.

Rule: when testing a bug that involves a specific encoding layer,
write the test at that layer. Integration tests are for end-to-end
flows, not for pinpointing serialization gaps.

## The fix shape matched the bug shape

The original review goal was "reduce 4 fields to 2". The fix achieved
the spirit of that goal without actually removing any field: persist
exactly one field (`MergedFrom`), treat the other three as caches, and
rebuild them at load time from `MergedFrom` + tree structure. This is
strictly better than "reduce to 2 persisted fields" because it keeps
the wire format minimal and all the derivation logic in one place
(`rebuildMergeState`), where future maintainers will see it.

Rule: when you're tempted to delete code that works, ask first whether
you can demote it instead. A correctly-computed cache is cheaper to
maintain than re-deriving the same logic at three call sites.

## Protobuf "no protocol changes" was aspirational

The design doc stated "no protobuf or protocol changes" as a goal. The
snapshot bug forced us to revisit that goal and realize: adding an
optional field to a protobuf message is not a breaking change. It's
backwards- and forwards-compatible at the wire level. The aspirational
goal should have been stated as "no breaking protocol changes", which
is still respected.

Rule: protobuf evolution has more room than the prose of a design doc
might suggest. Optional field additions, especially on the read path,
are almost always safe.

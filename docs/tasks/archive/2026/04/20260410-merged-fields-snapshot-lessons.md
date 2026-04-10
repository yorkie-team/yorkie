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

## "Derivable" requires an immutable source

The simplification attempt looked sound on paper: `mergedAt` should
equal `source.removedAt` because the merge tombstones the source in
the same operation, so the tickets coincide. Remove the field, read
`source.removedAt` at use time, done.

CodeRabbit caught the flaw: `TreeNode.remove()` overwrites
`removedAt` via LWW when a later concurrent tombstone targets the
same (already-tombstoned) node. `source.removedAt` is therefore not
a stable witness of the merge — it decays into "the latest tombstone
ticket that touched this node". Reading it at use time gives the
wrong answer for editors whose version vector covers the merge but
not the later delete, silently flipping the `SplitElement` Fix 8
branch.

The existing test suite did not exercise this corner case, so the
simplification commit passed CI. The bug was caught only by a careful
review of the code itself.

Rule: before treating a field as "derivable" from another, verify the
source is **immutable** after its first write. If the source can be
overwritten later — even under a well-understood protocol like LWW —
the derivation introduces action-at-a-distance coupling. The
correct move is to persist the immutable witness explicitly, not to
re-read a mutable field and hope the timing works out.

Corollary: absence of a test failure is not proof that a
simplification is safe. Tests prove behavior for the scenarios they
cover; they cannot prove that no scenario exists where the
simplification is wrong. For CRDT convergence work in particular,
the space of scenarios is too large for tests alone to gate
correctness — close reading of invariants matters.

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

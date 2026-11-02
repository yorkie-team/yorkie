**Created**: 2026-10-25

# Lessons: Pin how a reader decodes CHANGE_TYPE_UNSPECIFIED presence

Issue: #2230

## What the finding got right

A test that feeds an enum value outside `PresenceChange_ChangeType_name` only
ever reaches the one branch that looks that name up. It cannot speak for the
defined values that fall through the same `default` arm, and `0` is exactly
the value the rollout hazard is about. "There is a test near the code" is not
"the behaviour is covered"; the assertion has to touch the value the prose
names.

## What the finding got half right

It asked whether the real behaviour is "a silently-decoded empty change (not
an error)". It is, and the consequence it feared — the sender's presence being
wiped — is real: `inner.Change.Execute` routes an empty change type through
`default`, which stores the change's nil presence. But the comment in
`capabilities.go` was not describing the Go reader at all; the design doc it
points to describes the JS SDK throwing `ErrInvalidType`. The defect was the
comment generalising to "readers", not the Go converter contradicting it.

## Why the behaviour was pinned rather than fixed

`FromPresenceChange` is the read path for stored rows as well as for the wire,
so turning `CHANGE_TYPE_UNSPECIFIED` into an error would fail the load of a
document a pre-capability server had already written — trading a lossy
presence for an unreadable document. The existing comment in `from_pb.go`
makes that choice explicitly. Changing it is a behaviour change that belongs
with a migration story, not with a test-adequacy fix.

## Self review

Not run: this run has no tool that can launch the reviewer subagent, so the
round was skipped rather than performed. Stated here so a skipped round is not
read as a clean one. Review falls to CI, `@claude review`, and a human.

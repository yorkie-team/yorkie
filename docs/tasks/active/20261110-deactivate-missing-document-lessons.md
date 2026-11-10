# Deactivate a client whose attached document is gone — Lessons

**Created**: 2026-11-10

## Notes

- Skipping the missing document is not enough. `DeactivateClient` refuses
  while any entry in `documents` is attached or attaching, so the entry has
  to be written detached, the way a detach writes it.
- The client row records an attachment in three places: `attached_docs`,
  `attaching_docs` and the `documents` map, which the server reads. A manual
  cleanup that pulled only `attached_docs` left every client stuck, and the
  ones caught mid-attach were never in `attached_docs` at all.
- Integration helpers hardcode `mongodb://localhost:27017`; run them in a
  scratch worktree pointed at a test container instead of editing the branch.
  `TestHousekeeping` is `amd64`-only and does not run on an arm64 laptop.

## Self review

Round 1 (correctness and tests): no blocking findings.

- A `DocInfo` with only `ID` and `ProjectID` is enough for
  `UpdateClientInfoAfterPushPull` in both backends; neither reads `Key` or
  the seqs on the detach path.
- Added: the missing document's index arrays are asserted empty, and an
  entry left `attaching` is covered.
- Known limitation, not changed: the memory backend path has no test of its
  own, since `Deactivate` needs a cluster client.

Round 2 (design fit): two blocking findings, both about what the new caller
assumes of collaborators it reuses.

- Absence from one `FindDocInfosByIDs` batch is not proof the row is gone, and
  marking an entry detached locally skips the only writer of the
  presence-Clear change and of `RemoveOnDetach`
  (`clusterServer.DetachDocument`). Each candidate is now confirmed with a
  point read, and one the point read finds goes back to the ordinary detach
  path — which also closes the lagging-secondary gap noted in round 1.
- `UpdateClientInfoAfterPushPull` returns without writing when this node's
  cached client row already carries the entry it would write, so a cached copy
  from before a re-attach — status detached, same epoch — would swallow the
  detach and leave the stored row attached. The cached copies are dropped
  before the writes and the row is read back after them, so a swallowed write
  fails loudly instead of looking like a detach that landed.

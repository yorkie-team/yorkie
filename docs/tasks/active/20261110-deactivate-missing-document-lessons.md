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
- Known limitations, not changed: with a secondary read preference in the
  connection URI, a lagging secondary could make an existing document look
  missing; it would then be marked detached without a cluster detach (no
  presence clear). The default is primary. The memory backend path has no
  test of its own, since `Deactivate` needs a cluster client.

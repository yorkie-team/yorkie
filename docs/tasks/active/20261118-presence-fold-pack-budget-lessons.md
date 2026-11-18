# Lessons: the presence fold's size bound is per put, not per pack

**Created**: 2026-11-18

## Notes

- The per-put bound and the per-pack budget are the same measure
  (`presenceSize`) applied at two scopes. Keeping the over-budget path
  identical to the no-base path meant no new error, no new client contract
  and no new SDK behavior: the client already knows how to answer
  `ErrPresenceBaseUnavailable` by resending full puts.
- Only folded puts are charged. A put the client sent itself is already paid
  for in request bytes, so charging it would refuse honest packs without
  removing any amplification.
- `/self-review` was not run: this run is granted no tool that can dispatch a
  reviewer subagent. The PR's reviewers are CI, `@claude review` and a human.

## Unrelated CI failure seen on this branch

The integration lane failed once on
`TestAdmin/document_event_propagation_on_removal_test`
(`test/integration/admin_test.go:159`) while every other lane passed. The
branch does not reach it:

- The only product change is the `presence.Patch` arm of
  `foldPresencePatches` (`server/packs/presence_patch.go`). The subtest
  pushes no presence patch — it attaches, has an admin force-remove the
  document, and syncs — so the walk takes the same path it took before.
- `RemoveDocumentByAdmin` (`server/rpc/admin_server.go:720`) publishes
  `DocChanged` *after* `documents.RemoveDocument` has detached the watching
  client, so the removal can tear the client's watch stream down before the
  event reaches it. The test's reader fails on `io.EOF` rather than waiting,
  which is the shape of the observed failure: a fast subtest failure with the
  package's later tests all passing.

That race is in the watch/removal path, outside this PR's diff, so it is left
for a separate change.

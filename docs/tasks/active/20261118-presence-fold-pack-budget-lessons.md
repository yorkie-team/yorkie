# Lessons: the presence fold's size bound is per put, not per pack

**Created**: 2026-11-18

## Notes

- The per-put bound and the per-pack budget are the same measure
  (`presenceSize`) applied at two scopes, but they cannot share the same
  failure path. Reusing `ErrPresenceBaseUnavailable` for the budget looked
  free — no new error, no new client contract — and was not: a missing base
  is a property of server state that one full put clears, while passing the
  budget is a property of the pack's shape. The error names no patch, so the
  resend the client makes is over the budget too and the whole push,
  operations included, is refused again. The budget therefore degrades
  instead of refusing: past it the patches of a run share the single put its
  last change carries, which bounds the pack's folded output to the budget
  plus one put while the push still lands the first time.
- A run ends at a put or clear the client sent, because that change replaces
  the presence anyway. That is what keeps the coalesced puts self-paying:
  each one is bounded by the presence of the put that started its run.
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

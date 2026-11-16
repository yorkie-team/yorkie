# Deactivate a client whose attached document is gone

**Created**: 2026-11-10

Issue: [#2343](https://github.com/yorkie-team/yorkie/issues/2343)

## Problem

`clients.Deactivate` reads the documents the client row lists as attached or
attaching and fails with `find documents for detachment, expected: N,
actual: M` when a document row is missing. Nothing changes the row, so
housekeeping picks the client again every cycle and fails the same way: the
client is never deactivated. `DeactivateClient` also refuses while any entry
in `documents` is attached or attaching, so skipping the missing document is
not enough.

## Plan

- [x] Reproduce: attach, delete the document row out of band, deactivate
      (integration, `test/integration/deactivate_test.go`)
- [x] Mark each missing document detached in the client row through
      `ClientInfo.DetachDocument` and `UpdateClientInfoAfterPushPull`, the
      write a detach makes, and log it
- [x] Keep detaching the documents that do exist
- [x] `make verify`, integration lane for the new test and
      `test/integration/housekeeping_test.go` (amd64-only, so CI runs it)
- [x] Self review (max 3 rounds): round 1 clean, stopped

**Created**: 2026-11-18

# MongoDB new projects drop auto_revision_enabled and channel_session_ttl

Issue: #2224

## Problem

`mongo.Client.CreateProjectInfo` builds the insert document field by field
and omits `auto_revision_enabled` and `channel_session_ttl`. The returned
`ProjectInfo` is the in-memory struct from `database.NewProjectInfo`, so
`CreateProject` answers `autoRevisionEnabled: true` / `channelSessionTtl: 15s`
while the stored document has neither. Every later read decodes the missing
fields as the zero value, so automatic revisions never run for projects
created on MongoDB and the per-project channel session TTL silently falls
back to the server-wide value.

`ensureDefaultProjectInfo` sets `auto_revision_enabled` but also omits
`channel_session_ttl`, so the `default` project has the same TTL gap.

## Decision

Take the **default on** option from the issue: keep
`NewProjectInfo.AutoRevisionEnabled = true` (it matches the `default`
project, the memory backend and the published docs) and fix the MongoDB
insert so the stored document carries the defaults.

## Plan

- [x] Build the project insert document from one place so a newly added
      `ProjectInfo` field cannot be dropped from `CreateProjectInfo` only.
- [x] Use it for both `CreateProjectInfo` and `ensureDefaultProjectInfo`.
- [x] Unit test (no MongoDB): every `bson` field that `NewProjectInfo`
      sets to a non-zero value appears in the insert document.
- [x] Shared testcase run by both backends: create a project, read it back,
      and assert the create response equals the stored/read value for
      `AutoRevisionEnabled` and `ChannelSessionTTL`.

## Out of scope

- Migrating projects already stored on MongoDB without
  `auto_revision_enabled`. They keep reading as off; `UpdateProject` turns
  them on. There is no migration tool in this repo to hook into.
- Docs under yorkie-team.github.io (separate repository); they already
  document `auto-revision-enabled` default `true`.

# Lessons: MongoDB new project defaults (#2224)

## Notes

- `ProjectInfo` has a full set of `bson` tags, so the hand-written insert
  document in `mongo.Client.CreateProjectInfo` was duplicated state: any
  field added to the struct had to be added there too, and
  `auto_revision_enabled` (#1582) and `channel_session_ttl` were both
  missed. Centralising the document and guarding it with a reflection
  test is what stops the next field from being dropped.
- The insert cannot simply marshal the struct: `_id` must be left to
  MongoDB so it generates an ObjectID, while `ensureDefaultProjectInfo`
  pins `_id` to the fixed default project ID in the upsert filter.

- Storing `channel_session_ttl` is a behaviour change, not only a
  persistence fix: `channel.Manager` cleanup reads the project TTL and
  only falls back to the server-wide `Backend.ChannelSessionTTL` when the
  stored value is empty or unparsable. A MongoDB project created after
  this change therefore runs on `NewProjectInfo`'s 15s instead of the
  operator's `--channel-session-ttl`. That is what the memory backend
  already does and what
  `docs/design/per-project-channel-session-ttl.md` specifies for
  `NewProjectInfo`, so both backends now agree; an attempt to special-case
  MongoDB by storing the field empty made the two disagree on one field
  and broke `RunUpdateProjectInfoTest`, which asserts the create response
  carries `DefaultChannelSessionTTL`.
- `NewProjectInfo` seeded `ChannelSessionTTL` from the package constant,
  so once the MongoDB insert stored it, `--channel-session-ttl` reached no
  new project on either backend. The review panel upheld this twice and the
  loop stopped. The maintainer chose to leave the field empty in
  `NewProjectInfo`, so new projects inherit the server-wide value the way
  `ChannelBatchWindow` already does. A per-project field with a server-wide
  fallback should default to empty; copying a constant into it hides the
  fallback.

## Self review

Round 1 (correctness/tests, design fit): two findings, both on
`channel_session_ttl` being persisted by the MongoDB insert. The fix
first tried — clearing the field on insert — contradicted the design
doc's default for `NewProjectInfo` and the shared `UpdateProjectInfo`
testcase, so the insert now stores the default like every other field
and the backends stay identical. The operator-config gap it was aimed at
is recorded above and in the todo's out-of-scope list.

Round 2 (test coverage): the shared testcase only exercised
`CreateProjectInfo`, while the other caller of the project document,
`ensureDefaultProjectInfo`, now stores `channel_session_ttl`,
`channel_batch_window` and `updated_at` on the `default` project with
nothing reading them back. `RunCreateProjectInfoDefaultsTest` grew a
second subtest that ensures the default project and asserts both the
value the upsert answers with and the value a later
`FindProjectInfoByName` decodes, so the upsert path is covered on both
backends rather than only through the shared helper's unit test.

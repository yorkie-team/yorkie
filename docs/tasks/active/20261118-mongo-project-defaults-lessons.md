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
  stored value is empty or unparsable. Writing `NewProjectInfo`'s
  hardcoded 15s into every insert would therefore retire the operator's
  `--channel-session-ttl` for new projects, which
  `docs/design/per-project-channel-session-ttl.md` keeps as "the default
  seed for new projects and the fallback". So the MongoDB insert clears
  the field (`newProjectInfoCandidate`) and stores it empty: the project
  carries no TTL of its own until the admin API sets one, and the
  server-wide value governs meanwhile. The returned info matches what is
  stored, which is the invariant the rest of this change is about.
- `NewProjectInfo` seeds `ChannelSessionTTL` from the package constant
  rather than the operator's configured value, so the memory backend
  still pins new projects to 15s. Closing that needs
  `server/backend/database/project_info.go` and the config plumbing into
  both backends — a change outside this one.

## Self review

Round 1 (correctness/tests, design fit): two findings, both on
`channel_session_ttl` being persisted by the MongoDB insert. Fixed as
described above; the shared testcase now compares `ChannelSessionTTL`
round-trip (create answer vs. stored value) instead of asserting the
package constant, so it holds for a backend that leaves the field unset.

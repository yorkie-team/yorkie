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

## Self review

Not run: this change was produced in an autonomous one-shot run with no
reviewer subagent available. Review is left to CI, `@claude review` and a
human reviewer on the pull request.

# Lessons: Redact project SecretKey from users below Admin

## The leak was in the layer that resolves the role, not the one that prints it

`converter.ToProject` has no idea who is asking, so a fix there could only be
unconditional, and the dashboard genuinely needs the key for owners and admins.
The role is known exactly twice: `ProjectAndRole` returns it, and
`ListProjects` can look it up per membership. Clearing the field at those two
points leaves the converter dumb and keeps the gate next to the authorization
decision it depends on.

## Redacting in `ProjectAndRole` is safe because no caller reads the key

All nine call sites in `server/rpc/admin_server.go` use the returned project
for `project.ID` or pass it to `documents.FindDocInfoByKey`. The only readers
of `Project.SecretKey` in the tree are `admin/auth.go` (client-side, from a
project the caller already holds) and `server/packs/pushpull.go` (project from
the API-Key context, not from this function). So the redaction is invisible to
every existing flow except the response bodies it is meant to change.

## `ListProjects` needs a per-project role lookup

`ListProjectInfosByMember` returns `ProjectInfo` values with the membership row
already dropped, so the role is not available to the caller. Re-reading it with
`FindMemberInfo` costs one query per member project, which is bounded by the
number of projects a user has been invited to. The owned list needs no lookup:
being in it *is* the owner role.

## Half the escalation chain stays open by design

The API-Key-scoped procedures still authorize on the key alone. This change
stops a `member` from *obtaining* the key through the API; it does not make
those procedures role-aware, and it does not invalidate a key a member already
copied. `RotateProjectKeys` is the remedy for the latter, and the former is
recorded as a known limitation in both task's lessons files.

## Review rounds

Not run: `/self-review` dispatches a reviewer subagent, and this autonomous run
is granted no tool that can launch one. The branch's review is CI, `@claude
review` and a human on the PR. Recording the skip rather than letting it read
as a clean round.

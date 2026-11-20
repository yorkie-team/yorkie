# DeleteAccount leaves orphan memberships behind

**Created**: 2026-11-19

Issue: https://github.com/yorkie-team/yorkie/issues/2214

## Problem

`users.DeleteAccountByName` (`server/users/users.go:116`) calls only
`be.DB.DeleteUserInfoByName`, which removes the user document and nothing
else. Every `members` row that names the deleted user survives it, and those
rows can no longer be reached by name:

- `members.Remove` (`server/members/members.go:63`) starts with
  `FindUserInfoByName`, which now answers `ErrUserNotFound`, so `RemoveMember`
  fails and the orphan row can never be deleted.
- `members.List` (`server/members/members.go:42`) reads `FindUserInfoByID` for
  every row and returns the error, so one self-deleted member makes
  `ListMembers` fail for the whole project — the owner cannot even see who is
  left.

So a user who self-deletes (`DeleteAccount` is password-verified and
unauthenticated by design, `server/rpc/admin_server.go:120`) leaves a
membership the owner can neither list nor remove. If that user was an `Admin`,
they may also have copied the project's secret key, and the owner's view of
"who holds a role that may read the key" is now wrong and unfixable.

## Scope

In: account deletion removes the user's membership rows, in both the memory
and the Mongo database.

Out: automatic secret-key rotation. `docs/design/admin-credentials.md` already
decided rotation stays a deliberate owner action, because the same key signs
the event webhook; `RotateProjectKeys` remains the remedy. Also out: projects
the deleted user *owns* (the `projects.owner` field), which is a separate
pre-existing gap with no membership row involved.

## Steps

1. `database.Database`: add `DeleteMemberInfosByUser(ctx, userID)`.
2. `memory`: delete every `members` row on the `user_id` index.
3. `mongo`: `DeleteMany` on `{user_id: userID}`.
4. `users.DeleteAccountByName`: resolve the user, drop the memberships, then
   delete the user. Memberships first — if the second step fails, the account
   survives without its roles, which beats an unreachable orphan row.
5. Tests: shared database testcase for the new method (memory + Mongo), and a
   `server/users` test that account deletion clears the membership and leaves
   `members.List` working.
6. Docs: note the role-loss path in `docs/design/admin-credentials.md`,
   changelog entry.

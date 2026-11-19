# Lessons: DeleteAccount leaves orphan memberships behind

**Created**: 2026-11-19

## Findings while reading the code

- The issue quotes `members.go:135` (`revokeSecretKey`) and
  `projects.RotateSecretKey`, which come from PR #2196 and are not on `main`:
  `server/members/members.go` is 103 lines and has no such function. So the
  half of the finding that assumed a secret-key-only rotation exists cannot be
  implemented here; the remedy on `main` is still `RotateProjectKeys`.
- The orphan membership is worse than the issue states. Besides blocking
  `members.Remove`, it breaks `members.List` outright: the loop at
  `server/members/members.go:42` turns a missing user into an error for the
  whole listing, so one self-deleted member hides every other member of the
  project from the owner.
- Deletion order matters and neither database gives a transaction across the
  two collections here. Memberships are deleted first so a failure of the
  second step leaves an account without roles (recoverable by re-inviting)
  rather than a membership row no API path can reach.

## Review rounds

Not run locally: this change was produced in a headless one-shot run, which is
granted no tool that can launch the reviewer subagent `/self-review` requires.
Review is left to CI, `@claude review` and a human on the PR — no local round
was run and none should be read as clean.

### CI review panel, round 1 (security lens)

- Deleting the memberships before the user is not enough on its own. Nothing on
  the member write path re-checks that the user exists (`invites.Accept` upserts
  a row keyed by user ID alone), so an `AcceptInvite` overlapping the deletion
  re-creates the orphan the change removes. `DeleteAccountByName` now sweeps
  again after the user document is gone; a request starting later cannot reach
  the write, because the admin interceptor resolves the caller by username on
  every call. The upsert already in flight when the second sweep runs stays
  open — closing it needs the member write to verify the user, which Mongo
  cannot do atomically across collections, and the one obvious place to put that
  check (`UpsertMemberInfo`) is relied on by tests that upsert fabricated user
  IDs.
- The threat table overclaimed. A project records its owner as a bare user ID
  with no `members` row, so an owner who self-deletes leaves a project nobody
  owns: rotation falls to a remaining `Admin` member, and a project with none
  cannot rotate at all. There is no ownership transfer and no `DeleteProject` to
  fix it from outside, so it was recorded as an unmitigated risk.

### CI review panel, round 2

- The member write does have to verify the user, and it can. `UpsertMemberInfo`
  now resolves the user on both sides of its write: memdb serializes write
  transactions so the check before the insert is enough there, and Mongo pairs
  a check before with a check after that deletes the row it just wrote. The
  pair closes the race with the second sweep rather than narrowing it — a row
  written before the user document goes is swept, and one written after it is
  reverted by its own re-check, whose read necessarily follows the deletion.
  The tests that upserted fabricated user IDs (`projects_test.go`) now create
  the users; relying on them was the reason the check was skipped the first
  time, and it was not a good one.
- Invites were the other writer nobody swept. `invites.Accept` grants the
  invite's role without consulting its creator, so a link an admin created
  before deleting the account kept handing out membership on a user ID that no
  longer exists. `DeleteInviteInfosByCreator` sweeps them with the
  memberships. memdb has no `created_by` index on `invites`, so the memory
  backend filters a full scan, like `DeleteExpiredInviteInfos` does.
- Refusing beats documenting for the owner case. Rather than leaving a live
  project with an owner ID naming no user, `DeleteAccountByName` now returns
  `ErrOwnedProjectExists` while the account owns any project. It is the only
  remedy that fits inside this change — ownership transfer and `DeleteProject`
  are new API surface — and it keeps the account, which can still be handed
  over, instead of stranding the project.

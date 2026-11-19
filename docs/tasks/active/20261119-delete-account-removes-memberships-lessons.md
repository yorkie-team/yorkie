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
  fix it from outside, so it is recorded as an unmitigated risk rather than
  papered over.

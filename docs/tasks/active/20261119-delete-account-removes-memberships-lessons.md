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

Not run: this change was produced in a headless one-shot run, which is granted
no tool that can launch the reviewer subagent `/self-review` requires. Review
is left to CI, `@claude review` and a human on the PR — no round was run and
none should be read as clean.

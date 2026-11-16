**Created**: 2026-11-16

# Match GitHub Login Users by Auth Provider

**Goal:** GitHub login finds and creates users by `username` and
`auth_provider: "github"`, not by `username` alone, so it never returns a
user that another auth provider created.

**Out of scope:** storing the numeric GitHub ID, a provider interface,
external identity providers.

## Plan

- [x] Add `database.AuthProviderGitHub` and `ErrUserAuthProviderMismatch`
- [x] mongo: filter the upsert on `username` + `auth_provider: github`;
      on a duplicate key, look the GitHub user up without upsert (a
      concurrent first login may have made it) and return
      `ErrUserAuthProviderMismatch` only when it is absent
- [x] memory: same rule (existing row with another provider → error)
- [x] auth handler: answer the mismatch with 409 instead of a generic 500
- [x] DB testcase run by both mongo and memory: create, re-login keeps
      the ID, concurrent first logins share one user, a non-GitHub user of
      the same name is left untouched
- [x] Handler test: new user gets a session, returning user signs in
      again, a name held by a local user gets 409 and no session
- [x] `make verify`; mongo testcase under `-tags integration`
- [x] Self review (round 1: one blocking issue fixed; round 2: none)

## Notes

- No migration: existing GitHub rows already have `auth_provider:
  "github"`, and local rows have no `auth_provider`, which the new filter
  does not match.
- Behavior change: a local user who also used GitHub login under the same
  name can no longer use GitHub login for that user; the password login
  still works.

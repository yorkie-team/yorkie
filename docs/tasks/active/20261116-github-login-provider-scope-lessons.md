**Created**: 2026-11-16

# Match GitHub Login Users by Auth Provider — Lessons

- Look up users from an external provider by provider and ID, not by a
  name that local signup and the default user share.
- A MongoDB upsert that loses an insert race is retried by the server only
  when the filter equals the unique index key. With `auth_provider` added
  to the filter, concurrent first logins end in a duplicate key, so a
  duplicate key alone does not mean the name belongs to another provider:
  look again before reporting a mismatch.

## Self review

### Round 1 (correctness, tests)

- BLOCKING, fixed: concurrent first logins of one new GitHub user on
  MongoDB got 409 (the server stopped retrying the upsert, see above).
  Added a concurrent testcase, red before the fix and green ×5 with
  `-race`.
- Confirmed: legacy rows with no `auth_provider`, `""`, `null` or another
  value are not matched; existing GitHub rows still sign in; password
  login and member lookups are unchanged.

### Round 2 (design fit, the fix)

- No blocking findings; review stops here.
- Verified: `IsDuplicateKeyError` and `errors.Is(ErrNoDocuments)` see
  through `%w`; `findAndModify` runs on the primary, so the retry sees the
  winning insert; coverage shows the retry's success path runs.
- Applied: interface comment states the provider rule, retry comment
  wording, helper doc comment.

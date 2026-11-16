# Add an external login provider verifying signed assertions

**Created**: 2026-11-16

Issue: #2379 (part of #2377). Stacked on #2380 (provider interface).

## Problem

Self-hosting operators want the dashboard to sign in with their
organization's SSO. Those systems often do not follow OAuth2/OIDC, so the
login protocol stays in an operator-run broker. Yorkie verifies a short-lived
ES256 JWT the broker signs and posts through the browser.

The design lives in yorkie-team/second-brain
`03_projects/docs/design/external-identity-login.md`.

## Plan

Two pull requests.

### A. Identify users by provider and subject

An external provider's stable user ID (an employee number, say) is not the
username people invite by, so users need a subject next to the username.

- [x] `UserInfo.Subject`; local users and the default user get their
      username, GitHub users their login
- [x] Backfill `subject = username` for users without one at every start,
      before the indexes are built
- [x] Unique `(auth_provider, subject)` index, partial on `subject` existing
      so that an older server writing users without a subject during a
      rolling update does not collide
- [x] `GetOrCreateUserInfoByProvider(provider, subject, username)` finds by
      `(provider, subject)`; a new user whose username is taken is refused
      (D-2); a returning user keeps the username it was created with
- [x] `Identity.Subject`; the manager refuses an identity without one
- [x] A provider login that misses its user backfills that one user's
      subject (same provider, no subject yet) and looks again, so a user an
      older server wrote during a rolling update is not locked out
- [x] Tests: subject lookup, kept username, taken username, local and
      default user subject, people racing for one username; mongo backfill
      of legacy GitHub and local users, and of a GitHub user written after
      start

### B. External provider

- [ ] Config `Auth.ExternalProviders` (Name, DisplayName, LoginURL, Issuer,
      Audience, PublicKeys); a flag to read it from a file and helm values
- [ ] Refuse empty, duplicate, or reserved (`github`) provider names, and
      non-P-256 keys, at start
- [ ] `/auth/external/{name}/login` and `/auth/external/{name}/callback`
      (POST form field `assertion`)
- [ ] Assertion checks: ES256 only, `iss`, `aud`, `exp - iat <= 60s`, not
      expired, `jti` single use, `state`
- [ ] Bind the state to the browser with a cookie set at login and checked
      in `consumeState` (all providers)
- [ ] `GET /auth/providers`
- [ ] Tests with a fake broker: expiry, replay, wrong aud/iss, state
      mismatch, unsupported alg, username collision, key rotation
- [ ] User docs: assertion format, broker requirements, signing snippet,
      account migration example

## Rolling update note (A)

A user created by an older server during the update has no subject until
a new server backfills it: at start for all users, or at that user's next
provider login.

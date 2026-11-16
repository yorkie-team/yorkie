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

- [x] Config `Auth.ExternalProviders` (Name, DisplayName, LoginURL, Issuer,
      Audience, PublicKeys); `--auth-external-providers-file`; helm
      `yorkie.args.externalProviders` through a ConfigMap
- [x] Refuse invalid or duplicate names, bad URLs, and non-P-256 keys at
      start. External users get `auth_provider: "external:<name>"`, so a
      provider named `github` cannot reach GitHub users, which makes a
      reserved-name list unnecessary
- [x] `/auth/external/{name}/login` and `/auth/external/{name}/callback`
      (POST form field `assertion`)
- [x] Assertion checks: ES256 only, `iss`, `aud`, `exp - iat <= 60s`, not
      expired, `iat` not in the future, `jti` single use, `state`; 5s leeway
- [x] Bind the state to the browser with the `__Host-yorkie-auth-state`
      cookie set at login and checked in `consumeState` (all providers,
      GitHub too); cleared with the same attributes at the callback
- [x] The callback answers 303 (was 307, which re-posts the assertion to
      the dashboard); urlencoded bodies up to 64 KiB only; at most 10,000
      pending logins; `LoginURL` https or loopback http
- [x] A new user's username must be a valid Yorkie username (the sign-up
      rule), else 400
- [x] `GET /auth/providers`; GitHub listed only when its client ID is set
- [x] Tests with a fake broker: expiry, lifetime, future iat, missing
      claims, wrong aud/iss, unknown key, RS256/HS256/none, replay, forged
      and forwarded state, GET callback, username collision and validity,
      key rotation, config validation, file loading, providers listing.
      Mutation-checked: disabling the lifetime, `jti`, or cookie check fails
      its test
- [x] Design doc `docs/design/external-identity-login.md` with the
      assertion format, broker requirements, signing snippet, and migration
      example. The homepage self-hosting docs follow the release

## Behavior changes (B)

- GitHub login now also sets the `__Host-yorkie-auth-state` cookie and
  needs it at the callback, and its callback answers 303 instead of 307. The cookie is set and read on the Yorkie API host only, so
  it is first-party. A login started before the upgrade and finished after
  it fails once with `invalid state`; starting again works.
- `NewAuthHandler` returns an error; the server refuses to start on an
  invalid external provider.
- The 409 body is `Username is already used by another account`.

## Rolling update note (A)

A user created by an older server during the update has no subject until
a new server backfills it: at start for all users, or at that user's next
provider login.

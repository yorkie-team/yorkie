# Move GitHub login behind a provider interface

**Created**: 2026-11-16

Issue: #2378 (part of #2377, external identity login for the dashboard).

## Problem

The dashboard GitHub login is wired into the auth manager: the routes, the
OAuth config, the token exchange, the user lookup, and the GitHub-only upsert
all live in `AuthManager`. An external provider (#2379) has nowhere to plug in
without copying the state, upsert, and cookie handling.

## Goal

GitHub login becomes one implementation of a `Provider`. `AuthManager` keeps
only the shared work: issuing and consuming the login state, finding or
creating the user under the provider, and setting the session cookie. No
behavior change for users: the same routes, config keys, status codes, and
cookie.

## Plan

- [x] `Provider` (`Name`, `LoginURL`, `Callback`) and `Identity` in
      `server/rpc/auth/provider.go`
- [x] `githubProvider` in `server/rpc/auth/github.go`; `Callback` consumes the
      state through a function the manager passes, before the token exchange
      as before
- [x] `AuthManager` routes `/auth/{route}/login` and `/auth/{route}/callback`
      to registered providers; `/auth/github/*` is the `github` route.
      `AuthManager` is the `http.Handler` itself so tests can register a fake
      provider
- [x] The manager refuses an identity when the provider never consumed a
      valid state, so a provider bug cannot become login CSRF, and an
      identity without a username
- [x] Provider errors go to the log; the browser gets a fixed message
- [x] A state is bound to the route that issued it and refused after its
      10-minute lifetime (before, any unexpired-by-cleanup state worked on
      any callback)
- [x] `GetOrCreateUserInfoByGitHubID` becomes
      `GetOrCreateUserInfoByProvider(provider, username)` in mongo and
      memory; an empty provider is refused because local users have one
- [x] Tests: invalid and reused state, unknown route, a provider that skips
      the state, provider error details kept out of the body, empty username,
      a fake provider signing in, users of two providers, empty provider
- [x] `make lint`, unit tests, mongo integration tests for the database
      package (local mongo:8)

## Not in scope

- Binding the state to the browser (a state cookie checked in
  `consumeState`). States are not tied to the browser on main either, so
  login CSRF by forwarding a callback URL is possible today. #2379 adds it,
  because a forwarded assertion has the same problem
- Validating provider names at registration (empty, duplicate, or reserved
  such as `github`, which would share accounts with GitHub users). Only the
  built-in GitHub provider is registered here; #2379 adds the check with the
  external provider config

- `subject`, external provider config, assertion checks, `/auth/providers`:
  #2379
- Moving the `GitHub*` config keys; they stay as they are

## Behavior notes

Status codes, routes, config keys, and the cookie are unchanged. Response
bodies changed:

- An invalid or reused state answers `invalid state` (was `Invalid state`).
  Details a provider wraps around it go to the log.
- Failures talking to GitHub answer `Failed to sign in` and log the cause
  (were `Token exchange failed`, `Failed to get user info`, and so on). A
  future provider's errors may carry details of its identity source, so the
  manager never writes them to the browser.
- The 409 body no longer names GitHub.
- New: the manager refuses an identity without a username with a 500.
- New: a state presented at another provider's callback, or older than 10
  minutes, answers `invalid state`.

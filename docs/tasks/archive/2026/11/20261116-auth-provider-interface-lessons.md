# Move GitHub login behind a provider interface — Lessons

**Created**: 2026-11-16

- Where the state lives decides the interface. GitHub sends the state in the
  query, but a signed assertion carries it inside the token, which only the
  provider can open. Passing a `consumeState` function into `Callback` lets
  each provider check it at the right point, and the manager can still verify
  that some state was consumed. It cannot verify that it was the state in the
  request, so the provider contract has to say so.
- Generalizing the GitHub upsert to any provider opened a new hole: an empty
  provider matches local users, whose `auth_provider` is empty. A refactor
  that widens a lookup key needs a test for the value that collides with
  existing rows.
- A state store shared by every provider accepts a state at a callback it
  was not issued for. Recording the route with the state costs one field.

## Self review

- Round 1 (correctness, security, tests; one reviewer, 7 tool calls):
  nothing blocking. Fixed: wrapped `errInvalidState` details reached the 400
  body; states were not bound to their route and were usable past their
  lifetime until a later login cleaned them up; the provider contract said to
  consume the state before verifying, which is wrong for a signed token;
  comments overclaimed login CSRF protection. Added tests for each, and the
  forged-state test now asserts that the code was never exchanged. Deferred
  to #2379 with reasons in the todo: binding the state to the browser,
  validating provider names. Not changed: `LoginURL` cannot keep per-login
  data such as a PKCE verifier; neither GitHub nor a broker needs it. Stopped
  after round 1 since it had no blocking findings.

**Created**: 2026-10-12

# Lessons: keyless API-Key on the admin path

## RED on main (180780d9)

Unit test `TestAdminServiceAuthenticateAPIKey` (memory DB backend, default
project ensured): all five keyless params (empty, `" \t "`, `" "`,
`"  \t"`, `"  "`) authenticate with `UseDefaultProject=true`;
`authenticate` returns no error, where `CodeUnauthenticated` +
`ErrSecretKeyNotProvided` is expected. With `UseDefaultProject=false` they
were already rejected.

```text
Error: Not equal: expected: 0x10 actual: 0x2
Error: Expected error with "secret key is not provided" in chain but got nil.
Messages: non-breaking space key with UseDefaultProject=true
--- FAIL: TestAdminServiceAuthenticateAPIKey
```

Integration test `TestRESTAPI/keyless_api_key_test` over real HTTP against
`ListDocuments`:

```text
expected: 401 actual: 200   authorization "API-Key  "
expected: 401 actual: 200   authorization "API-Key   "
expected: 401 actual: 200   authorization "API-Key    "
```

## The wire changes which keyless forms reach the guard

Over HTTP/1.1, `API-Key ` and `API-Key  ` already came back 401 on main:
net/http trims trailing ASCII whitespace off the header value, leaving
`API-Key`, which `SplitN` rejects as an invalid format. Only a param that
net/http does not trim (U+00A0) reached the fail-open branch. So the
issue's `curl -H 'authorization: API-Key '` example is not the live vector
over HTTP/1.1; U+00A0 from #2161 is. A unit test of the interceptor alone
would have overstated the plain-space case, and an HTTP test alone would
have missed the in-process path. Test both layers when a header parser sits
between the attacker and the guard.

## Check why a fail-open fallback exists before removing it

The empty-key fallback came from #1475, written for admin callers that
would reach the default project without a key after #1471 removed
`projectName`. No current caller does: the Go admin client sends the key
`GetProject` returns, the dashboard sets `API-Key` only with a non-empty
key, and the MCP handler already rejects an empty key.

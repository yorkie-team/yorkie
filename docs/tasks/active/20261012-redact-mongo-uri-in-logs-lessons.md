**Created**: 2026-10-12

# Redact the MongoDB URI before logging it — lessons

## Moving a value into a Secret reclassifies every place it is printed

#2176 moved the connection URI into a Kubernetes Secret, so reading the
password now needs `get secrets`. Nothing in that diff touched the two Info
lines that print the URI, but those lines became the weakest path to the
password the moment the Secret defined what "weak" meant: `kubectl logs` is
granted far more widely. When a value is promoted to a secret, the work is
not done until every sink that already carries it is audited.

## `net/url` cannot be trusted to find the userinfo of a secret

`url.Parse` handles the shapes a correct MongoDB URI takes — the comma-joined
multi-host authority, `mongodb+srv`, a percent-encoded password — but a
password is only *supposed* to be percent-encoded, and the one that is not is
the case redaction exists for. `url.Parse` cuts the fragment and then the
query off the whole URI before it looks for the authority, so
`mongodb://user:pa#ss@h1:27017` parses as host `user:pa` (`pa` is a valid
optional port) with fragment `ss@h1:27017` and *no* userinfo at all;
`URL.Redacted` then has nothing to mask and echoes the password in full. The
userinfo is now cut from the raw string: candidate `@`s from the right, the
first one whose following segment is spelled like a host list and whose
prefix spans no `/`. A parser that is lenient about where a delimiter may
appear is the wrong tool for deciding which half of a string is the secret.

## gosec flags the redacted URI too

G101 ("Password in URL") matches any `scheme://user:pass@host` string
literal, so the test's *expected* value `mongodb://admin:xxxxx@…` tripped it
just as the input did. Building both from a concatenated userinfo constant
keeps the cases readable without a `//nolint` directive.

## `URL.Redacted` covers the userinfo password and nothing else

`Redacted` masks `u.User`'s password and re-emits `RawQuery` verbatim, but a
MongoDB connection string carries credentials in the option string too:
`tlsCertificateKeyFilePassword` (and its legacy alias
`sslClientCertificateKeyPassword`), `proxyPassword`, and
`authMechanismProperties`, which holds the AWS session token and the OIDC
access token. The option string is scrubbed before `Redacted` runs. Names are
matched case-insensitively and on a substring (`password`, `token`, `secret`,
`credential`) so an alias or a later addition is masked by default; the
options that name no secret in their name need an explicit entry.

## An unparsable URI has to be dropped, not echoed

The tempting fallback for a URI the redaction cannot take apart is to log it
as-is so the operator can see what is wrong. That is exactly the case where
the password cannot be located, so it is exactly the case where echoing it
leaks. The fallback reports only that the URI did not parse, and it now also
covers a scheme-opaque URI and one left holding an `@` that closes no
userinfo the cut could find.

## A test that copies a server timeout inherits a deadline it does not want

The integration lane failed in `TestClient_RotateProjectKeys` after exactly
5.00s — the `ConnectionTimeout` that `setupTestWithDummyData` copied from the
server default, not any assertion in the test. 5s is right for a server that
should fail fast at startup instead of hanging; a test binary dials a cold
database — connect, ping, then `ensureIndexes` over every collection — while
`go test ./...` drives the same mongod from the other packages under `-race`,
and that overruns 5s on load alone. `require.NoError` then reports a slow
database as a failed test. The package's dial budget is now 30s, so an
unreachable database still fails the test, only later.

`test/helper`'s `MongoConnectionTimeout` and the dials in `server/authz`,
`test/complex` and `test/integration` carry the same 5s and the same latent
sensitivity; none of them failed, and they are outside this change.

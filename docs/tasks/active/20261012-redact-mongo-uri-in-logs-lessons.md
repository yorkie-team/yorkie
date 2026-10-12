**Created**: 2026-10-12

# Redact the MongoDB URI before logging it — lessons

## Moving a value into a Secret reclassifies every place it is printed

#2176 moved the connection URI into a Kubernetes Secret, so reading the
password now needs `get secrets`. Nothing in that diff touched the two Info
lines that print the URI, but those lines became the weakest path to the
password the moment the Secret defined what "weak" meant: `kubectl logs` is
granted far more widely. When a value is promoted to a secret, the work is
not done until every sink that already carries it is audited.

## `net/url` handles MongoDB URIs, including the multi-host form

`mongodb://u:p@h1:27017,h2:27017/db?replicaSet=rs0` parses: `url.Parse`
validates only the text after the *last* colon as a port, so the comma-joined
authority survives and `URL.Redacted` round-trips it unchanged. `mongodb+srv`
and a percent-encoded password are fine too. A hand-rolled "cut at the last
`@`" was not needed.

## gosec flags the redacted URI too

G101 ("Password in URL") matches any `scheme://user:pass@host` string
literal, so the test's *expected* value `mongodb://admin:xxxxx@…` tripped it
just as the input did. Building both from a concatenated userinfo constant
keeps the cases readable without a `//nolint` directive.

## An unparsable URI has to be dropped, not echoed

The tempting fallback for a URI `url.Parse` rejects is to log it as-is so the
operator can see what is wrong. That is exactly the case where the password
cannot be located, so it is exactly the case where echoing it leaks. The
fallback reports only that the URI did not parse.

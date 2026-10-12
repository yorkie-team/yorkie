**Created**: 2026-10-12

# Redact the MongoDB URI before logging it

Issue #2183. The server prints its MongoDB connection URI verbatim at Info
level twice during startup, and that URI carries the database user's
password.

## Problem

`mongo.Dial` logs `conf.ConnectionURI` (`server/backend/database/mongo/
client.go:177`) and `backend.New` logs the same string as `dbInfo`
(`server/backend/backend.go:211-215`). The cluster chart's default log level
is `info` (`build/charts/yorkie-cluster/values.yaml`), so every pod writes

```text
MongoDB connected, URI: mongodb://admin:<password>@…-mongos…, DB: yorkie
```

to stdout, where `kubectl logs` — or whatever ships those logs onward —
reads it. #2176 moved that URI into a Secret precisely so the password needs
`get secrets` to read; the log line hands it to a far weaker permission.

## Plan

- [x] Add `(*mongo.Config).RedactedConnectionURI()`: parse the URI and mask
      the password (`net/url`'s `URL.Redacted`), keeping scheme, username,
      hosts and options so the line stays useful. A URI that does not parse
      is replaced wholesale, never echoed, since the userinfo cannot be
      located to strip it.
- [x] Use it at both log sites.
- [x] Unit-test the helper: password masked, no-credential URI untouched,
      `mongodb+srv`, multi-host, percent-encoded and unparsable input.

## Verification

- [x] `go test ./server/backend/database/mongo/... ./server/backend/...`
- [x] `make lint`
- [ ] Full `make test` / integration lane — left to CI; needs MongoDB.

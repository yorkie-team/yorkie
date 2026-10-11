**Created**: 2026-10-12

# Stop Signing Admin Tokens with a Public Default Key — Lessons

Plan: `20261012-jwt-default-secret-todo.md`.

## RED evidence

`server/secret_key_test.go` and a new subtest in `cmd/yorkie/server_test.go`
on unmodified `origin/main` (180780d9):

```text
secret_key_test.go:83:  Error: Should be empty, but was yorkie-secret
secret_key_test.go:87:  Error: Should not be: "yorkie-secret"
secret_key_test.go:92:  Error: An error is expected but got nil.
secret_key_test.go:105: Error: Should NOT be empty, but was
secret_key_test.go:106: Error: Should NOT be empty, but was
secret_key_test.go:107: Error: Should not be: ""
--- FAIL: TestAdminSecretKey/unset_key_never_resolves_to_a_public_key_test
--- FAIL: TestAdminSecretKey/unset_key_is_generated_per_server_test
--- PASS: TestAdminSecretKey/explicit_key_is_kept_test
--- FAIL: TestServerCmdFlagDefaults/backend-secret-key_has_no_default
        Error: Should be empty, but was yorkie-secret
```

Line 92 is the issue itself: a token forged offline with `"yorkie-secret"`
for `admin` verifies against the key a server started from the sample
config resolves.

## The empty key was a second hole

The `"yorkie-secret"` fallback lives only in the config-file path
(`ensureBackendDefaultValue`) and in the CLI flag default. A config built in
code (`server.NewConfig()`, embedders, the test helper with the key cleared)
skipped both and reached `auth.NewTokenManager` with an empty key, which is
just as public. So the fallback cannot live in config parsing: `server.New`
is the one place every path goes through.

## Unit-testing server.New without a network

`server.New` with the sample config dials the StarRocks DSN it names, so the
test clears `Mongo`, `Kafka` and `StarRocks`. `Shutdown` on a never-started
server returns "remove cluster node ...: document not found" because the
node was never registered; the test ignores that error.

## Why the chart requires the key instead of generating one

The first plan was a `lookup`-and-`randAlphaNum` default persisted in a
Secret. Production deploys the chart through Argo CD, which renders with
`helm template`, where `lookup` always returns nothing. Every sync would then
write a new random key; running pods keep the env value they started with, so
any pod restarted after a sync disagrees with its siblings and admin requests
fail intermittently. A required value is deterministic under every renderer.
`secretKeyExistingSecret` lets the operator keep the key out of the values
file in git.

## Every replica must share the key

`TokenManager.Verify` checks only against the local key. `admin/client.go`
sets `x-shard-key` only on `RemoveDocumentByAdmin` (hashed by document), so
`LogIn` and the next `ListProjects` can land on different pods. A per-process random key is therefore only correct for a single node,
which is what the server warning says.

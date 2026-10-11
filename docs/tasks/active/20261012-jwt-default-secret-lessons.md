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
`LogIn` and the next `ListProjects` can land on different pods. A
per-process random key is therefore only correct for a single node, which
is what the server warning says.

## Self-review round 1

Findings from the coordinator's review of fe44f347..2ff92984, and what
changed:

- A. The chart passed the key as `--backend-secret-key
  $(YORKIE_BACKEND_SECRET_KEY)`. Kubernetes expands that into argv, so the
  key showed in `/proc/<pid>/cmdline` and the pprof cmdline endpoint (the
  exposure #2083/#2148 track). The CLI had no env convention (only the MCP
  helper reads `YORKIE_SECRET_KEY`, a project key), so `server.New` now
  reads `YORKIE_BACKEND_SECRET_KEY` when the flag and config file leave the
  key empty, and the chart sets only the env var. It sits in `server.New`,
  not the CLI, so a config file that omits the key gets it too; flag and
  file values win.
- B. A blank key slipped through as "unset" and each pod generated its own.
  Decision: refuse, not generate. A blank configured value or a
  present-but-blank env var means the operator meant to share a key; a
  per-process fallback turns that mistake into intermittent login failures
  instead of a pod that will not start. The chart also fails to render on a
  blank `secretKey`; it cannot see inside an existing Secret, so the server
  check covers that case.
- C. The pod template now carries `checksum/backend-secret-key` when the
  chart owns the Secret, so a key change rolls every replica.
- D. An explicit `"yorkie-secret"` was still accepted. Decision: refuse at
  startup. It is the value the issue is about, a warning in a log nobody
  reads leaves the hole open, and no repo in the workspace passes it
  explicitly (grep over 03_projects), so nothing legitimate breaks.
- E. Wrapped two lines over 80 columns in the task files.

RED for A, B, D on fe44f347..2ff92984 (the explicit-wins subtest passes
before and after, as a guard):

```text
--- FAIL: TestAdminSecretKey/unset_key_is_read_from_the_environment_test
--- FAIL: TestAdminSecretKey/blank_key_is_rejected_test
--- FAIL: TestAdminSecretKey/blank_key_in_the_environment_is_rejected_test
--- FAIL: TestAdminSecretKey/public_key_is_rejected_test
```

All eight subtests pass after the fix.

## CodeRabbit round (PR #2176)

- `--backend-secret-key=""` given explicitly was indistinguishable from an
  absent flag once it reached `backend.Config`, so it fell through to the
  env var or a random key. The CLI now checks `Flags().Changed` and refuses
  a blank value before anything else runs. A config-file `SecretKey: ""`
  stays "unset", because `config.sample.yml` ships it that way and existing
  configs copy it.
- The test drives `RunE` with `--config` pointing at a missing file, so a
  run that passes the key check fails on the config read instead of
  starting a server. RED on 07a75eca: the error was "read config file ...
  no such file or directory", not the key error, for both `""` and `"  "`.
- An operator-managed Secret can rotate without the chart seeing it, so no
  checksum can roll the pods. Documented a `rollout restart` in the chart
  README and values instead of adding a reloader.

## Review panel round (PR #2176)

- The chart README told the reader to add the key to every `helm install`
  example but left the two main ones unchanged, so following them verbatim
  hit the template's `fail`. Both examples now pass `secretKey`, and the
  upgrade section carries the command plus a migration note: an existing
  release has no key, and `--reuse-values` does not invent one, so every
  already-installed release fails to upgrade until its operator sets one.
- `checksum/backend-secret-key` was dropped from the pod template. It is an
  unsalted SHA-256 of the signing key, and a pod annotation is readable with
  `get pods`, which is far weaker than `get secrets` — a hash is enough to
  search for the key offline. Rolling pods on a key change is a documented
  `rollout restart` for both key sources now, same as the existing-Secret
  path already needed.
- `validateSecretKey` accepted any non-blank key that was not byte-for-byte
  `yorkie-secret`. Two holes: `kubectl create secret --from-file` keeps the
  file's trailing newline, so migrating the old default into a Secret
  produced `"yorkie-secret\n"` and passed; and a one-character key was
  accepted for HS256, which an attacker breaks offline against any issued
  token without the server seeing an attempt. The key is now trimmed, the
  public default is matched case-insensitively after trimming, and a key
  shorter than 32 characters (RFC 7518 §3.2, the HMAC-SHA256 block) is
  refused. Trimming also keeps a node reading the key from a file matching
  one reading it from a flag.
- Open, not fixed here: a deployment that is multi-replica but does not use
  the chart (raw manifests, an operator, compose scale-out) still gets a
  different generated key per pod, with only a log line to say so — the
  message is now at Error level and names the load-balancer symptom, but
  nothing fails. Failing closed instead would stop `make start-server`,
  `build/docker/docker-compose*.yml` and the k6 stack, which are outside
  this change. Also open: `DefaultAdminUser`/`DefaultAdminPassword` are
  still `admin`/`admin`, reaching the same admin takeover from
  `EnsureDefaultUserAndProject` in `server/backend/backend.go`.
- The chart's render-time guard only checked that the key was non-blank while
  the server had grown two more rules, so `--set yorkie.args.secretKey=hunter2`
  installed cleanly and then CrashLoopBackOff'd every pod with an error only
  visible in the logs. The guard now mirrors `validateSecretKey`: trim, then
  reject blank, the public default case-insensitively, and anything shorter
  than 32 characters. The Secret stores the trimmed value, so what the chart
  validated is what the server reads. A chart-side copy of a server-side rule
  is duplication worth paying for: the alternative is a failure that surfaces
  one layer away from the input that caused it.
- The README told operators to pass `--set yorkie.args.secretKey="$(openssl
  rand -base64 32)"` on every `helm upgrade`, which mints a new key each time.
  With the checksum annotation gone, nothing rolls the pods, so the Secret
  holds key B while the pods hold key A and any pod that restarts alone
  disagrees with its siblings. Upgrade examples now reuse the key in place
  (`secretKeyExistingSecret`, or read back from the Secret), and generating a
  new one is a separate, deliberate "Rotating the key" step ending in
  `rollout restart`.
- `DefaultAdminPassword` is gone. An unset admin password now resolves the
  same way the signing key does: `YORKIE_BACKEND_ADMIN_PASSWORD` first, then a
  generated password logged once at startup. The password only matters while
  `EnsureDefaultUserAndProject` creates the account, so an existing database
  keeps the account it has; what changes is that a fresh install no longer
  ships the credential published here. The chart grew
  `yorkie.args.adminPasswordExistingSecret`, handed over the environment for
  the same reason as the key, because the generated-and-logged password is a
  credential sitting in a log pipeline.

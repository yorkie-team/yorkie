**Created**: 2026-10-12

# Stop Signing Admin Tokens with a Public Default Key

**Goal:** Issue #2103. When `Backend.SecretKey` is empty, `ensureBackendDefaultValue`
and the `--backend-secret-key` flag fall back to `DefaultSecretKey =
"yorkie-secret"`, a constant anyone can read on GitHub. The admin JWT is HS256
signed with that key, and the interceptor trusts the `username` claim, so
anyone who can reach AdminService can forge a token for `admin`. The
`yorkie-cluster` Helm chart passes no `--backend-secret-key`, so a chart
deployment signs with the public key.

## Constraints

- No publicly known key is ever used implicitly.
- Zero-config `yorkie server`, `build/docker` compose and tests keep working.
- Every node of a cluster must share the key: most admin RPCs carry no shard
  key, so the gateway may route a token minted on one node to another, and
  `TokenManager.Verify` checks against the local key.

## Plan

- [x] RED: unit tests in `server/` showing that an unset key resolves to
      `"yorkie-secret"` (sample config / bare `Backend:` section) and that a
      token forged with `"yorkie-secret"` verifies against it.
- [x] Remove `DefaultSecretKey`. Leave the key empty through config parsing
      and the CLI flag default; resolve it once in `server.New`, the single
      point every path (CLI flags, config file, `NewConfig()`, tests) goes
      through. Empty -> 32 bytes from `crypto/rand`, and a warning that
      tokens do not survive a restart and that clusters must set the key.
- [x] Test helper uses its own explicit test key (multi-server integration
      tests need a shared key).
- [x] `config.sample.yml` and flag help: document the random fallback.
- [x] Helm `yorkie-cluster`: `yorkie.args.secretKey` (chart creates the
      Secret) or `yorkie.args.secretKeyExistingSecret` (operator-managed
      Secret); passed to the pod as an env var expanded in `args`. Neither set
      -> the chart fails to render (`lookup`-generated default rejected; see
      lessons).
- [x] `make verify`, relevant integration tests, `helm lint`/`helm template`.

## Review

- RED -> GREEN: `TestAdminSecretKey` (3 subtests) and
  `TestServerCmdFlagDefaults/backend-secret-key_has_no_default` fail on
  180780d9 and pass with the fix.
- `make verify`: 0 lint issues, unit tests green.
- Integration: `-run 'TestAdmin|TestUser|TestRESTAPI'`, 29 passed.
- Manual: `yorkie server` with no flags logs the warning, `LogIn` +
  `ListProjects` work with the issued token, and a token forged with
  `"yorkie-secret"` gets `unauthenticated`.
- Helm: `helm lint` passes with a key; `helm template` fails with the
  message when neither value is set, creates the Secret in value mode and
  references the named Secret in existing-secret mode.

Rollout note: every admin token issued before this change was signed with
`"yorkie-secret"` and stops verifying once a real key is set, so dashboard
and CLI users log in again. The devops values must set
`secretKeyExistingSecret` (with the Secret created) before bumping the chart.

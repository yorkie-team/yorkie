# Keep the GitHub OAuth client secret off the command line

**Created**: 2026-10-12

Follow-up to #2176, filed as #2177.

## Problem

The `yorkie-cluster` chart enables pprof unconditionally
(`--pprof-enabled`, `deployment.yaml`), and the profiling server registers
`/debug/pprof/cmdline` (`server/profiling/server.go`). Anything that can reach
the profiling port — the Prometheus scrape path, any pod in the mesh, any
sidecar — can read the server's full argv.

#2176 moved the admin token signing key, the MongoDB connection URI and the
cluster secret into the credentials Secret and handed them to the container as
environment variables. `--auth-github-client-secret` was left behind: the chart
still renders it as an argument, so the OAuth client secret is readable through
the same endpoint.

## Scope

Only the GitHub OAuth client secret. `--starrocks-dsn` can also carry a
password, but the issue does not name it and the chart has no Secret path for
it; noted in the PR body instead.

## Steps

- [ ] `cmd/yorkie/server.go`: add `GitHubClientSecretEnv`
      (`YORKIE_AUTH_GITHUB_CLIENT_SECRET`), read when the flag is unset, the
      way `MongoConnectionURIEnv` and `ClusterSecretEnv` are.
- [ ] `cmd/yorkie/server_test.go`: extend the credential-env and
      flag-wins tests to cover it.
- [ ] `server/config.sample.yml`: note the env var next to
      `GitHubClientSecret`.
- [ ] Chart: carry `githubClientSecret` in the `<name>-credentials` Secret,
      pass it as the env var, drop the argument.
- [ ] Chart `values.yaml` + `README.md`: document the new Secret key and env
      var alongside the other three.

## Acceptance

- `helm template` with `yorkie.args.githubClientSecret` set renders no
  `--auth-github-client-secret` argument, and the value appears only in the
  Secret and in a `secretKeyRef`.
- A server with the flag unset picks the secret up from the environment; a
  server given the flag still wins over the environment.
- `make lint` and `go test ./cmd/...` green.

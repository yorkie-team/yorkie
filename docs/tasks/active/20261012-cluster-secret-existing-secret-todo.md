# Read the cluster RPC secret from an existing Secret

**Created**: 2026-10-12

Issue: #2192. Follow-up to #2135 (ClusterService open with an empty secret)
and #2176 (`secretKeyExistingSecret` for the admin token key).

## Problem

`yorkie.args.clusterSecret` is the only way to give the chart the cluster RPC
secret, so a deployment that keeps its values in git leaves it empty, and
ClusterService accepts every request through the ingress gateway.

## Plan

- [x] File the issue (#2192)
- [x] `values.yaml`: add `yorkie.args.clusterSecretExistingSecret`, the name
      of a Secret holding the secret under `cluster-secret`; it takes
      precedence over `clusterSecret`
- [x] `backend-secret-key.yaml`: refuse the chart-owned
      `<yorkie.name>-credentials` name, as for `secretKeyExistingSecret`; stop
      storing `cluster-secret` when the existing Secret is used
- [x] `deployment.yaml`: read `YORKIE_CLUSTER_SECRET` from the existing
      Secret when it is set
- [x] `README.md`: document the value and the rollout caveat (pods with the
      secret refuse cluster calls from pods without it until the rollout ends)
- [x] Verify with `helm template`: defaults and inline `clusterSecret` render
      unchanged; existing Secret wires the env var; the chart-owned name and
      the shared-Secret case behave as intended
- [x] Self review, open the PR

## Review

`helm template` results: defaults, inline `clusterSecret` and
`secretKeyExistingSecret` render byte-identical to `main`. An existing Secret
wires `YORKIE_CLUSTER_SECRET` to it and drops the inline value from the
chart-owned Secret. Both values may name the same Secret. The chart-owned name
fails the render. `helm lint` passes.

Self review, round 1 (correctness/docs): no blocking findings. Fixed the
rollout paragraph (a changed secret fails calls both ways; turning it on rolls
the Deployment by itself) and the stale Helm section of
`docs/design/cluster-service-auth.md`. Known limitation: a blank secret
(whitespace inline, or empty in the Secret) is trimmed to empty by the server
and leaves the service open, as before this change; the server warns at
startup.

---
title: cluster-service-auth
target-version: 0.7.6
---

# Cluster Service Authentication

## Problem

ClusterService (`/yorkie.v1.ClusterService/*`) is designed for inter-node communication within the Yorkie cluster. However, it is registered on the same HTTP mux and port as YorkieService and AdminService (`server/rpc/server.go:82-85`), with no authentication in the interceptor (`server/rpc/interceptors/cluster.go`).

In the current gateway-only Istio setup (no sidecar), both external and internal traffic flow through the same Istio Gateway:

```text
External: Client → ALB → Istio Gateway (Envoy) → Yorkie Pod
Internal: Yorkie Pod → Istio Gateway Service → Istio Gateway (Envoy) → Yorkie Pod
```

The VirtualService routes all `/yorkie.v1` prefixed paths, which includes ClusterService. Since external and internal requests share the same gateway, there is no infrastructure-level mechanism to distinguish them.

This means anyone on the internet can call ClusterService RPCs such as `DetachDocument`, `PurgeDocument`, and `InvalidateCache` through `api.yorkie.dev`.

Related: https://github.com/yorkie-team/yorkie/issues/1038

### Goals

- Block external access to ClusterService RPCs
- Keep internal cluster communication working (both unicast via gateway and broadcast via Pod IP)
- Minimal change scope: no gateway/Istio reconfiguration, no proto changes

### Non-Goals

- mTLS between cluster nodes (h2c is sufficient within VPC)
- Secret rotation mechanism (can be added later)
- Separating ClusterService to a different port

## Design

### Shared Secret Authentication

All Yorkie nodes in the cluster share the same secret. The cluster client sends the secret in a request header, and the cluster interceptor validates it. Requests without a valid secret are rejected.

### Configuration

Add `ClusterSecret` to `backend.Config`:

```go
// ClusterSecret is the shared secret for authenticating inter-node
// cluster RPCs. If empty, a random per-process secret is used.
ClusterSecret string `yaml:"ClusterSecret"`
```

Add CLI flag `--cluster-secret` (follows existing `--cluster-*` naming):

```go
cmd.Flags().StringVar(
    &conf.Backend.ClusterSecret,
    "cluster-secret",
    "",
    "The shared secret for authenticating cluster RPC calls.",
)
```

### Client Side (cluster/client.go)

Store the secret in `Client` and attach it to every request via header:

```go
const clusterSecretHeader = "x-cluster-secret"

type Client struct {
    conn          *http.Client
    client        v1connect.ClusterServiceClient
    isSecure      bool
    rpcTimeout    gotime.Duration
    clusterSecret string
}

func WithClusterSecret(secret string) Option {
    return func(o *Options) { o.ClusterSecret = secret }
}
```

Each RPC method already builds a `connect.Request`. Add the header before calling:

```go
func (c *Client) withClusterSecret(req connect.AnyRequest) {
    if c.clusterSecret != "" {
        req.Header().Set(clusterSecretHeader, c.clusterSecret)
    }
}
```

### Server Side (server/rpc/interceptors/cluster.go)

Add secret validation to `ClusterServiceInterceptor`:

```go
type ClusterServiceInterceptor struct {
    backend       *backend.Backend
    requestID     *requestID
    clusterSecret string
}

func (i *ClusterServiceInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
    return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
        if !isClusterService(req.Spec().Procedure) {
            return next(ctx, req)
        }

        if err := i.authenticate(req.Header()); err != nil {
            return nil, err
        }

        // ... existing metrics logic
    }
}

func (i *ClusterServiceInterceptor) authenticate(header http.Header) error {
    if i.clusterSecret == "" {
        return connect.NewError(connect.CodeUnauthenticated,
            errors.New("cluster secret is not configured"))
    }

    secret := header.Get(clusterSecretHeader)
    if subtle.ConstantTimeCompare([]byte(secret), []byte(i.clusterSecret)) != 1 {
        return connect.NewError(connect.CodeUnauthenticated,
            errors.New("invalid cluster secret"))
    }

    return nil
}
```

Key details:
- Use `crypto/subtle.ConstantTimeCompare` to prevent timing attacks
- Authentication fails closed: callers pass `Config.EffectiveClusterSecret()`,
  which falls back to a random per-process secret, so an empty secret reaching
  the interceptor means the server has no secret at all and every request is
  rejected
- A missing header is reported separately from a mismatched one, since that is
  the signature of a peer that was started without a cluster secret
- Apply to both `WrapUnary` and `WrapStreamingHandler`

### Helm Chart (build/charts/yorkie-cluster)

Pass `clusterSecret` in `values.yaml`. If set, the value is passed as `--cluster-secret` in the Deployment args. If empty, the flag is omitted and each replica generates its own secret, so inter-node RPCs fail: a multi-replica chart install has to set it.

```yaml
yorkie:
  args:
    # Generate with: openssl rand -base64 24
    clusterSecret: "<value>"
```

### Wiring

`ClusterServiceInterceptor` receives the secret from `backend.Config`:

```go
// server/rpc/server.go
clusterInterceptor := interceptors.NewClusterServiceInterceptor(be, be.Config.EffectiveClusterSecret())
```

`ClusterClientPool` passes the secret when creating clients:

```go
// server/backend/backend.go
cluster.WithClusterSecret(b.Config.EffectiveClusterSecret())
```

### Single-Node Mode

When running a single Yorkie server (no cluster), `ClusterSecret` is empty by
default. Both the cluster client and the interceptor then use a secret this
process generated with `crypto/rand` at first use, so the node still talks to
itself while every external caller is rejected.

The fallback is deliberately **not** `SecretKey`:

- `SecretKey` defaults to the published constant `yorkie-secret`, so any
  deployment that kept the default would have its ClusterService gated by a
  value anyone can read out of this repository. `ClusterService` is mounted on
  the public RPC port and its RPCs (`GetDocument`, `PurgeDocument`,
  `DetachDocument`, `Broadcast`) read the project out of the request message
  without going through the auth webhook, so that is a cross-project bypass.
- `SecretKey` signs admin tokens. Sending it in a plaintext header on every
  inter-node RPC - h2c by default - would expose it to any header-logging proxy
  on the path.

The generated secret is per process, so it cannot serve a real cluster. Nodes
that must reach each other have to be started with the same
`--cluster-secret`; `server/rpc/server.go` warns at startup whenever the
generated secret is in use. `Config.Validate()` additionally rejects
`--cluster-secret yorkie-secret`.

### Rolling Upgrade

The check fails closed, so a node running a build configured **without** any
cluster secret sends no `x-cluster-secret` header and is rejected by an upgraded
peer. Upgrading a running cluster therefore takes two steps:

1. Set the same `--cluster-secret` on every node **while still running the
   previous build**. The previous build already sends the header when a secret
   is configured and still accepts header-less requests, so this step is safe
   in both directions.
2. Roll out the new build with the same secret.

Skipping step 1 makes old -> new inter-node RPCs (`DetachDocument`,
`PurgeDocument`, `Broadcast`, `GetDocument`) fail with `Unauthenticated` -
reported as a missing cluster secret header - until every node has been
restarted. Client deactivation (`server/clients/clients.go`) is the most
visible casualty, since it detaches through ClusterService. Leaving
`--cluster-secret` unset does not end the outage once the rollout completes:
each node then has its own generated secret.

The same applies to any external user of the exported `cluster` package: it
must pass `cluster.WithClusterSecret`.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Secret transmitted in plaintext over h2c | Acceptable within VPC. If cross-VPC communication is needed, enable TLS with `--cluster-secure` (already exists) |
| Secret leaked in logs or error messages | Never log the secret value. Error messages say "invalid cluster secret", not the actual value |
| All nodes must share the same secret | Single config value in Helm values.yaml, deployed uniformly via ArgoCD. Left empty, the `yorkie-cluster` chart generates one and stores it in the `<name>-cluster-secret` Secret, reusing the stored value on upgrade, so the shipped multi-replica default is not a broken cluster |
| Secret readable from the pod spec | The chart passes it as a Secret-backed env var expanded into `--cluster-secret`, not as a literal container arg, so `get pod` shows only the reference. `clusterSecretExistingSecret` keeps it out of the Helm release's stored manifests entirely |
| Empty `ClusterSecret` | Falls back to a random per-process secret, so the endpoint is never open. Multi-node deployments break instead, loudly: `Config.UsesGeneratedClusterSecret()` reports it, the server warns at startup, and `prepareClusterClients` logs an error naming the node count once membership reports a peer |
| Default `SecretKey` | Irrelevant to this gate: the cluster secret never falls back to `SecretKey`, and `Validate()` rejects `--cluster-secret yorkie-secret` outright |
| `SecretKey` on the wire | The admin-token signing key is never sent as a cluster header |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Shared secret over mTLS | mTLS requires certificate management infrastructure. Shared secret is simple and sufficient for same-VPC communication |
| Header-based over metadata-based | Connect RPC uses HTTP headers. Consistent with existing `x-shard-key` pattern |
| Fall back to a random per-process secret when `ClusterSecret` is empty | Keeps single-node deployments working without new config and never leaves the public ClusterService endpoint guarded by a published constant. Multi-node deployments must configure the secret, and fail closed with a startup warning until they do |
| Constant-time comparison | Prevents timing side-channel attacks on the secret |
| Reject header-less ClusterService calls at the Istio gateway | Defence in depth, not a second gate: the server check still decides. Internal unicast also goes through the gateway (`--backend-gateway-addr` points at the gateway Service), so the path cannot simply be dropped - but no legitimate caller omits `x-cluster-secret`, so a 404 for those keeps anonymous internet callers off the handler. `x-cluster-secret` is left out of the CORS `allowHeaders` lists so a browser cannot be induced to send one |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Unconditional VirtualService path blocking | Internal unicast also goes through the gateway, so blocking `/yorkie.v1.ClusterService/` outright would block cluster communication too. The chart blocks only the header-less subset |
| Istio AuthorizationPolicy with source IP | Pod CIDR is dynamic. Fragile and breaks on node scaling |
| Separate port for ClusterService | Requires Helm chart, Service, and Istio changes. Much larger scope for the same result |
| mTLS between nodes | Certificate provisioning and rotation adds operational complexity. Overkill for same-VPC |
| API key per node | Unnecessary complexity. Nodes are homogeneous and trusted equally |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

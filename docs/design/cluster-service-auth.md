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
// cluster RPCs. If empty, all requests are allowed.
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
        return nil
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
- If `ClusterSecret` is empty, allow all requests (backward compatible)
- Apply to both `WrapUnary` and `WrapStreamingHandler`

### Helm Chart (build/charts/yorkie-cluster)

Set `clusterSecret` in `values.yaml`, or `clusterSecretExistingSecret` to read it from a Secret created out of band (key `cluster-secret`), which takes precedence. The Deployment hands it to the server as the `YORKIE_CLUSTER_SECRET` environment variable, never as `--cluster-secret` in the args, where `/proc/<pid>/cmdline` and the pprof cmdline endpoint would expose it. One of the two is required: if both are empty the chart fails the render, since this chart publishes ClusterService through the ingress gateway and a server without the secret allows every request. The chart cannot read a Secret it does not create, so `clusterSecretExistingSecret` is checked only for being set; a blank value stored there still leaves the server open, and only its startup warning says so.

```yaml
yorkie:
  args:
    # Generate with: openssl rand -base64 24
    clusterSecret: ""
    # Or: a Secret holding the value under `cluster-secret`
    clusterSecretExistingSecret: "yorkie-cluster-secret"
```

### Wiring

`ClusterServiceInterceptor` receives the secret from `backend.Config`:

```go
// server/rpc/server.go
clusterInterceptor := interceptors.NewClusterServiceInterceptor(be, be.Config.ClusterSecret)
```

`ClusterClientPool` passes the secret when creating clients:

```go
// server/backend/backend.go
cluster.WithClusterSecret(b.Config.ClusterSecret)
```

### Single-Node Mode

When running a single Yorkie server (no cluster), `ClusterSecret` is empty by default. The interceptor allows all requests when the secret is empty, maintaining backward compatibility with existing deployments.

Because an empty secret leaves ClusterService open on the public RPC port, `Yorkie.Start` logs a warning when `ClusterSecret` is empty. It does not refuse to start, so single-node and local setups keep working with the default.

### Trust Only IDs Off the Wire

A ClusterService handler trusts only the **identifiers** a request carries —
`project_id`, `document_id`, `client_id`, `document_key`. Every other property
of those entities is read back from the database; none is taken from the
request body.

This holds whatever `ClusterSecret` is set to. The secret is empty by default,
and while it is, the interceptor admits every request that reaches the public
RPC port, so a handler that reads a behavioural field off the wire lets an
unauthenticated caller choose that behaviour. `DetachDocument` is the example:
it decides on `Project.RemoveOnDetach` whether a detach also marks the document
removed, so taking that flag off the wire would let anything that reaches the
port turn a detach into a removal on a project that has the flag off.

Handlers therefore resolve the project themselves:

```go
// server/rpc/cluster_server.go
projectInfo, err := s.backend.DB.FindProjectInfoByID(ctx, types.ID(req.Msg.Project.GetId()))
if err != nil {
    return nil, err
}
project := projectInfo.ToProject()
```

Key details:
- Read the ID through its generated getter (`GetId()`), which is nil-safe.
  Direct field access, as `converter.FromProject` does, panics on a request
  that omits the message.
- `FindProjectInfoByID` is served from the project cache, so the read costs
  nothing in the steady state.
- Newer RPCs (`CompactDocument`, `PurgeDocument`, `RevalidateAccess`,
  `ListChannels`, …) take a plain `string project_id` and are the shape to
  copy. `DetachDocument` and `GetDocument` still carry a whole `Project`
  message, including `SecretKey`, for wire compatibility; their handlers read
  nothing from it but `id`, and the field is to be narrowed to `project_id` in
  a future proto change.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Secret transmitted in plaintext over h2c | Acceptable within VPC. If cross-VPC communication is needed, enable TLS with `--cluster-secure` (already exists) |
| Secret leaked in logs or error messages | Never log the secret value. Error messages say "invalid cluster secret", not the actual value |
| All nodes must share the same secret | Single config value in Helm values.yaml, deployed uniformly via ArgoCD |
| Empty secret allows all requests | Backward compatible. Operators must set a secret in production to enable authentication; the server logs a warning at startup while it is empty |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Shared secret over mTLS | mTLS requires certificate management infrastructure. Shared secret is simple and sufficient for same-VPC communication |
| Header-based over metadata-based | Connect RPC uses HTTP headers. Consistent with existing `x-shard-key` pattern |
| Allow when secret is empty | Backward compatible with existing deployments. Operators opt in to authentication by setting a secret |
| Constant-time comparison | Prevents timing side-channel attacks on the secret |
| No VirtualService changes needed | Authentication at server level works regardless of gateway topology. No coupling to Istio configuration |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| VirtualService path blocking | Internal unicast also goes through the gateway, so it would block cluster communication too |
| Istio AuthorizationPolicy with source IP | Pod CIDR is dynamic. Fragile and breaks on node scaling |
| Separate port for ClusterService | Requires Helm chart, Service, and Istio changes. Much larger scope for the same result |
| mTLS between nodes | Certificate provisioning and rotation adds operational complexity. Overkill for same-VPC |
| API key per node | Unnecessary complexity. Nodes are homogeneous and trusted equally |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

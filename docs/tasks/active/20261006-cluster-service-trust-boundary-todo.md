**Created**: 2026-10-06

# Harden the ClusterService Trust Boundary

**Goal:** Close #2135 and #2137 with the smallest non-breaking change.

- #2135: with `--cluster-secret` empty (the default), the cluster interceptor
  lets every request through, and ClusterService shares the public RPC mux,
  so `/yorkie.v1.ClusterService/*` is reachable by anyone who can reach the
  RPC port.
- #2137: `CompactDocument` and `PurgeDocument` lock by `DocumentKey` but act
  on `DocumentId` without checking the two name the same document.
  `DetachDocument` already has this guard.

## Investigation: can the Istio route be narrowed?

No. Unicast cluster calls (`Backend.ClusterClient`) dial
`Backend.GatewayAddr`, which the helm chart sets to
`<name>-gateway.<ns>.svc.cluster.local` — the Istio ingress gateway Service
(`helm install yorkie-gateway istio/gateway`). They rely on the gateway's
consistent hashing on `x-shard-key` to reach the node that owns the
document, so they pass through the same Gateway and VirtualService as SDK
traffic. Dropping ClusterService from the `/yorkie.v1` route would break
Detach/Compact/Purge/GetDocument between nodes. `cluster-service-auth.md`
already rejected VirtualService path blocking for this reason. Only the
broadcast path (Pod IP via `RPCAddr`) bypasses the gateway.

## Tasks

- [x] Confirm how nodes reach ClusterService (above). Skip the route change.
- [x] Log a warning at startup when `ClusterSecret` is empty; keep starting.
- [x] Say in the flag help, `config.sample.yml` and helm `values.yaml` that
      the secret must be set in production.
- [x] Note the startup warning in `docs/design/cluster-service-auth.md`.
- [x] Guard `CompactDocument` and `PurgeDocument` with
      `docInfo.Key != docKey` → `ErrDocumentKeyMismatch`, before mutating.
- [x] Integration test: both handlers reject an ID/key pair that names two
      documents and leave the target intact; accept the matching pair.
      Check it fails without the guard.
- [x] `make verify`; targeted integration tests with MongoDB up.
- [x] Self-review; log it in the lessons file.

## Follow-ups (out of scope)

- Serve ClusterService on a separate, internal-only port.
- Generate or require a secret in cluster mode instead of defaulting open.

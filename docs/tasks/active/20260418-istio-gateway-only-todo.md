**Created**: 2026-04-18

# Simplify Istio to Gateway-Only Mode

**Spec:** `03_projects/yorkie/docs/design/istio-to-envoy-gateway.md`

## Changes

6 files changed, net -33 lines. All changes in `build/charts/yorkie-cluster/`.

### 1. Split VirtualService routes and add header injection

- [x] `templates/istio/virtual-service.yaml`: Split single route into `/auth` (with `headers.request.set.x-shard-key: auth-session`) and `/yorkie.v1` (no header injection). Both keep CORS policy.

### 2. Remove Lua EnvoyFilter

- [x] `templates/istio/ingress-envoy-filter.yaml`: Remove `ingress-shard-key-header-filter` (Lua script). Keep `ingress-stream-idle-timeout-filter`.

### 3. Remove ambient mode

- [x] `templates/istio/waypoint.yaml`: Delete file
- [x] `templates/yorkie/service.yaml`: Remove `istio.io/use-waypoint` annotation block
- [x] `templates/yorkie/deployment.yaml`: Remove `sidecar.istio.io/inject: "false"` annotation block
- [x] `values.yaml`: Remove `istio.mode` setting

### 4. Minikube validation

- [x] Istio gateway-only install (base + istiod + gateway, no ambient)
- [x] Yorkie cluster deploy with 3 replicas + MongoDB
- [x] Auth path reaches Yorkie server (400 = body missing, server reached)
- [x] YorkieService routing works (401 = api key missing, server reached)
- [x] CORS preflight returns correct headers
- [x] Consistent hashing: same shard key routes to same pod

**Created**: 2026-10-06

# Lessons: Harden the ClusterService Trust Boundary

## Inter-node calls go through the ingress gateway

The obvious fix for #2135, dropping `/yorkie.v1.ClusterService/` from the
Istio VirtualService, would break the cluster. `Backend.ClusterClient` dials
`GatewayAddr`, which the helm chart points at the Istio ingress gateway
Service, so unicast cluster RPCs use the same Gateway, VirtualService and
`x-shard-key` consistent hashing as SDK traffic. Only broadcasts dial Pod
IPs directly. `cluster-service-auth.md` had recorded this when it rejected
path blocking. Read the design doc's Alternatives before redoing a rejected
option.

## The missing guard was worse than a wrong lock

Without the `docInfo.Key != docKey` guard, a `PurgeDocument` whose key
named another document did not just take the wrong lock: it purged the
target's changes, since the handler acts on `DocumentId`. The new test
showed the change count drop to zero before the guard was added.

## The cluster client drops the connect code

`cluster.Client` returns errors through `fromConnectError`, so the
`InvalidArgument` code of `ErrDocumentKeyMismatch` does not survive; only
the message does. Tests that go through the cluster client check the
message, as the existing cluster `DetachDocument` subtest does.

## Self-review

Round 1 (correctness/tests), over the full branch diff:

- Guards sit after `FindDocInfoByRefKey` and before `packs.Compact` /
  `packs.Purge`, the same place as in `DetachDocument`. A recreated
  document reuses its key with a new ID, so a legitimate pair still
  matches. No finding.
- Tests: both subtests fail without the guard (Purge actually wiped the
  target) and pass with it; the matching pair still compacts and purges.
- Non-blocking: `ErrDocumentKeyMismatch` says "change pack key does not
  match the document", though Compact/Purge carry no change pack. Kept to
  reuse the existing error; noted as a known limitation.
- Fixed: the test's top comment listed only the SDK handlers; it now names
  the cluster handlers too.

No blocking findings; stopped after round 1.

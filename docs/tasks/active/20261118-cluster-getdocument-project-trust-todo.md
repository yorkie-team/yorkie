**Created**: 2026-11-18

# Stop trusting the wire Project in ClusterService GetDocument

Issue: https://github.com/yorkie-team/yorkie/issues/2335

## Problem

`clusterServer.GetDocument` builds its project with
`converter.FromProject(req.Msg.Project)`. #2330 changed the sibling
`DetachDocument` to trust only `Project.Id` and load the stored project with
`FindProjectInfoByID`, so two handlers of the same service now follow
different trust models for the same field.

- A request that omits `project` makes `converter.FromProject` dereference nil
  and panic (`pbProject.Id` on a nil receiver).
- ClusterService shares the public RPC port and its interceptor accepts every
  request while `ClusterSecret` is empty — the default. So the panic is
  reachable unauthenticated on a default deployment.

## Plan

1. `server/rpc/cluster_server.go` — `GetDocument` reads only
   `req.Msg.Project.GetId()` (nil-safe) and resolves the stored project with
   `s.backend.DB.FindProjectInfoByID`, as `DetachDocument` and
   `RevalidateAccess` already do.
2. `docs/design/cluster-service-auth.md` — record the "trust only IDs off the
   wire" rule, so the next handler added to ClusterService follows it.
3. `test/integration/cluster_project_trust_test.go` — new integration test:
   - `GetDocument` with no `project` returns an error, not a panic, and the
     server keeps serving.
   - `GetDocument` with a forged project body (real ID, forged fields)
     resolves the stored project.
   - `DetachDocument` with a forged project body (real ID,
     `remove_on_detach: true` on a project that has it off) detaches without
     removing the document.

## Out of scope

- Replacing `ClusterServiceDetachDocumentRequest.project` /
  `ClusterServiceGetDocumentRequest.project` with `string project_id`. That is
  a proto change, and `make proto` cannot run in this environment; the
  generated code under `api/yorkie/v1/` must not be hand-edited. Filed as a
  follow-up note in the PR body.
- The default-open cluster interceptor
  (`server/rpc/interceptors/cluster.go`) — the issue calls it a separate
  concern.

## Acceptance criteria

- [ ] `GetDocument` reads only the project ID from the wire.
- [ ] A `GetDocument` request without `project` does not panic.
- [ ] The trust rule is written down in `docs/design/cluster-service-auth.md`.
- [ ] A test covers `DetachDocument` ignoring a forged project body.
- [ ] `make lint` and `go build ./...` green.

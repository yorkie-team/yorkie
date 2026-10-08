**Created**: 2026-10-08

# Enforce the Auth Scheme an Admin Method Expects

**Goal:** `AdminServiceInterceptor.authenticate` accepts both `Bearer` and
`API-Key` for every authenticated procedure. `Bearer` puts only a user in the
context, `API-Key` puts only a project, and the handlers read one or the other
with an unchecked type assertion. Calling a method with the scheme it does not
expect panics in the handler's first line, so the caller gets a connection
reset instead of an error. Make the interceptor reject the mismatched scheme
before the handler runs.

## Tasks

- [x] `isProjectScoped(procedure)` in `server/rpc/interceptors/admin.go`: the
      allow-list of methods that read `projects.From(ctx)`. An allow-list on
      the project side is fail-closed — a method added later is `Bearer`-only
      until it is listed, so it cannot inherit the gap.
- [x] `ErrSchemeNotAllowed` (PermissionDenied) next to `ErrSecretKeyNotProvided`.
      `authenticate` takes the procedure and returns it when `API-Key` is used
      outside the project-scoped set, or `Bearer`/session cookie inside it.
- [x] Second guard: `connect.WithRecover` on the handler options in
      `server/rpc/server.go`, so a future context/handler mismatch returns
      `Internal` instead of resetting the connection.
- [x] Unit test for `isProjectScoped`: every procedure in the generated
      `v1connect` constant list is classified, and the set matches the
      handlers that call `projects.From`.
- [x] Integration regression tests in `test/integration/restapi_test.go` for
      both directions: `API-Key` → `GetProject` and `Bearer` → `ListDocuments`
      both return a JSON error with `permission_denied`, not a reset.
- [x] `make lint`, targeted `go test`.

**Created**: 2026-10-08

# Enforce the Auth Scheme an Admin Method Expects — Lessons

Plan: `20261008-admin-auth-scheme-scope-todo.md`.

## Why the allow-list sits on the project side

The two scopes are not symmetric. A project-scoped method is one that reads
`projects.From(ctx)`, and that set is closed and small; everything else on
`AdminService` either reads `users.From(ctx)` or reads neither
(`GetServerVersion`). Listing the project-scoped methods means a method added
later defaults to `Bearer`-only: if it turns out to be project-scoped its
author sees `permission_denied` the first time they call it, instead of the
panic that #1582 and #1616 inherited. The reverse allow-list would default new
methods to accepting `API-Key`, which is the failure mode being fixed.

`GetServerVersion` needs authentication but reads neither scope. It falls on
the `Bearer` side, which is what `cmd/yorkie/version.go` already sends — it
calls the client with a plain context, and `admin.AuthInterceptor` only
switches to `API-Key` when the context carries a project.

## The scheme split was already the client's contract

`admin.AuthInterceptor` picks the scheme from `projects.HasProject(ctx)`, and
every `admin.Client` method that targets a project calls `projects.With` first.
So the server is being taught a rule the Go client has followed since #1471;
no client change is needed, and no correct caller loses a path that worked.

## Known limitation: the secret key carries no role

A project-scoped handler authorizes on the secret key alone — there is no user
in the context to check a role against, so `RemoveDocumentByAdmin`,
`UpdateDocument`, `CompactDocumentByAdmin`, `CreateSchema`, `RemoveSchema`,
`BroadcastByAdmin` and `RevalidateAccess` are reachable by anyone holding the
key. `GetProject` hands that key to every member: `ProjectAndRole`
(`server/projects/projects.go:89`) resolves owner, admin and member alike, and
`converter.ToProject` (`api/converter/to_pb.go:107`) copies `SecretKey` into
the response. A `member` can therefore read the key and act with the authority
of an owner.

That chain predates this change and is untouched by it. Before the scheme
check, these procedures already accepted `API-Key` on the same unchecked path,
and the `Bearer` alternative was never a working credential for them —
`projects.From` (`server/projects/context.go:30`) is an unchecked type
assertion, so a session token reached the handler and panicked on its first
line. The check converts that panic into `PermissionDenied`; it neither adds
nor removes a way to reach the handler.

Closing it needs work outside this change: withhold `SecretKey` from
`GetProject` for roles below owner, or give the project-scoped procedures a
credential that carries an identity to authorize. Both land in
`api/converter/to_pb.go` and `server/rpc/admin_server.go`.

## `connect.WithRecover` is a backstop, not the fix

The handler options are shared by the Yorkie, Admin and Cluster services, so
the recover covers all three. It turns a panic into `Internal` rather than a
`net/http` per-connection reset, which is strictly better for a caller behind a
proxy — but it reports a bug, it does not classify one. The interceptor check
is what makes the mismatched scheme a documented `PermissionDenied`.

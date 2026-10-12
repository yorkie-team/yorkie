**Created**: 2026-10-12

# Redact project SecretKey from users below Admin

Issue #2160. Follow-up filed by the review panel on #2158.

## Problem

`converter.ToProject` copies `project.SecretKey` into every `Project` wire
message (`api/converter/to_pb.go:107`), and two user-scoped handlers return it
with no role gate:

- `GetProject` (`server/rpc/admin_server.go:208`) calls
  `projects.ProjectAndRole`, which resolves owner, admin and `member` alike
  (`server/authz/authorization.go:65`), and returns the project as-is.
- `ListProjects` (`server/rpc/admin_server.go:192`) returns every project the
  user is merely a member of (`projects.ListProjects`).

The secret key is the whole credential of the API-Key-scoped AdminService
procedures (`RemoveDocumentByAdmin`, `UpdateDocument`,
`CompactDocumentByAdmin`, `CreateSchema`, `RemoveSchema`, `BroadcastByAdmin`,
`RevalidateAccess`), which authorize on `projects.From(ctx)` alone and carry no
user to check a role against. So a `member` reads the key out of a response it
is entitled to make and then acts with owner authority. This is exactly the
limitation recorded in
`docs/tasks/active/20261008-admin-auth-scheme-scope-lessons.md:30-53`.

## Approach

Make the secret key follow the authority it grants: only a caller whose role is
at least `Admin` may read it. The protobuf field stays (generated code cannot
change here); the value is emptied before it reaches the converter.

The gate goes in the business layer (`server/projects`), not in the handlers,
so every present and future caller of these two functions is covered:

- `ProjectAndRole` returns a copy with `SecretKey` cleared when the resolved
  role is below `Admin`. Its nine call sites use the project only for its ID
  and for document lookups; none reads the key.
- `ListProjects` keeps the key for owned projects and clears it for
  member-sourced ones whose membership role is below `Admin`.

`CreateProject` (caller is the owner), `UpdateProject` and `RotateProjectKeys`
(both already `CheckPermission(..., Admin)`) are unaffected.

## Steps

- [x] `types.Project.WithoutSecretKey()` — shallow copy with the key cleared.
- [x] `projects.ProjectAndRole` — redact when role < Admin.
- [x] `projects.ListProjects` — redact member projects whose role < Admin.
- [x] Unit tests in `server/projects` over the memory DB: owner, admin and
      member, through both functions.
- [x] Update the known-limitation section of
      `20261008-admin-auth-scheme-scope-lessons.md` to record that the leak
      half of that chain is now closed.
- [x] `make lint`, `go test ./server/projects/... ./api/types/...`.

## Out of scope

Adding user-role checks to the API-Key-scoped procedures themselves. They have
no user in context by construction; that is a separate design change. Keys
already read by a member are not invalidated — `RotateProjectKeys` exists for
that and remains the remedy.

## Known limitation: a member cannot drive the project-scoped admin client

`admin.Client` resolves the project with `GetProject` and then authenticates
every project-scoped call with `API-Key <project.SecretKey>`
(`admin/client.go:339`, `admin/auth.go:72`). With the key redacted, a `member`
sends an empty key and the server rejects it, so `ListDocuments`, `GetSchemas`,
`ListChangeSummaries` and `CreateDocument` stop working for that user through
the SDK and CLI — the read ones included.

This is the intended consequence, not a regression to patch here. Those
procedures cannot distinguish a member from an owner, so leaving the key
readable would keep granting owner authority to members, which is the bug this
change closes. Restoring member read access requires teaching the
project-scoped path to accept a user credential and check a role — a change to
`admin/auth.go`, `admin/client.go` and the server's API-Key interceptor that
belongs to its own issue.

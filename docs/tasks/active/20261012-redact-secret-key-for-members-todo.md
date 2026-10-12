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

## Session path for members

Hiding the key alone left a `member` with no credential for the project-scoped
procedures at all: `admin.Client` sent `API-Key <project.SecretKey>`
(`admin/auth.go`), the key was now empty, and #2174 rejects a keyless
`API-Key`. Read-only calls such as `ListDocuments` broke for members along with
the destructive ones, and the dashboard's document pages with them. The review
panel upheld this twice, so it is fixed here instead of deferred.

The project-scoped procedures now accept a second credential: a session token
(`Bearer`) together with the project named in the `x-yorkie-project` header
(`types.ProjectNameKey`). The interceptor resolves the user's role in that
project and checks it against a per-method minimum:

- `projectScopedMethods` (`server/rpc/interceptors/admin.go`) now maps each
  method to the least role it needs over a session. Reads are `Member`;
  anything that changes documents, schemas or sessions is `Admin`
  (`CreateDocument`, `UpdateDocument`, `RemoveDocumentByAdmin`,
  `CompactDocumentByAdmin`, `CreateSchema`, `RemoveSchema`,
  `BroadcastByAdmin`, `RevalidateAccess`).
- `Bearer` without the header is still `ErrSchemeNotAllowed`. A project the
  user cannot see and one that does not exist both answer
  `ErrProjectAccessDenied`, so the header cannot probe project names. An
  unknown role fails closed, as in `redactForRole`.
- The `API-Key` path is unchanged: the key is the project's own credential and
  carries its full authority, which is why only `Admin` and above may read it.
- `admin.AuthInterceptor` sends `API-Key` when the project it holds has a key
  and falls back to `Bearer` plus `x-yorkie-project` when it does not.

## Steps (session path)

- [x] `types.ProjectNameKey` header constant.
- [x] Interceptor: role table, `Bearer` + project name on project-scoped
      methods, `authorizeProject`.
- [x] `admin/auth.go`: fall back to the session when the key is empty.
- [x] Unit tests: owner/admin/member/stranger over a read and a write method,
      unknown project, missing header, invalid token, no key in a member's
      context.
- [x] Integration test: a member lists the project's documents through
      `admin.Client` and is denied `RemoveDocument` and `CreateDocument`.
      Red on the previous head (`ListDocuments` returned nothing), green now.
- [x] `make lint`, unit tests, integration suite.

## Out of scope

Keys already read by a member are not invalidated; `RotateProjectKeys` remains
the remedy. The dashboard (separate repository) still sets the key from
`GetProject`; it needs the same fallback (send the session cookie and
`x-yorkie-project` when the key is empty) before member-role users see their
documents there again.

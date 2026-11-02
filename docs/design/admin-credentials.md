---
title: admin-credentials
target-version: 0.7.25
---

# Admin API Credentials

## Problem

`AdminService` has two kinds of procedure. A user-scoped one reads the caller
out of the context (`users.From`) and answers about that user: `GetProject`,
`ListProjects`, `UpdateProject`. A project-scoped one reads the project
(`projects.From`) and answers about that project's documents, schemas and
channels: `ListDocuments`, `RemoveDocumentByAdmin`, `CreateSchema` and the rest.

Until now the project-scoped half accepted exactly one credential, the
project's secret key, sent as `Authorization: API-Key <secretKey>`. That key
names no user, so the procedures it reaches authorize on the project alone and
have no role to check. Meanwhile `GetProject` and `ListProjects` returned the
key to every user who could see the project, `member` included
(`api/converter/to_pb.go` copies `SecretKey` into the wire message).

The two facts compose into a privilege escalation: a `member` reads the key out
of a response it is entitled to make, then calls `RemoveDocumentByAdmin` with
the authority of the project's owner. Membership roles were decorative on this
surface.

### Goals

- The secret key reaches only callers whose project role already carries the
  authority the key grants.
- A `member` keeps the read-only project-scoped procedures it is entitled to.
- Every project-scoped procedure is gated on a role, whichever credential
  reached it.

### Non-Goals

- Revoking a key already copied. The key is a bearer credential; a copy
  outlives the role it was read under. Only `RotateProjectKeys` revokes it.
- Splitting the key's other meaning off (event-webhook signing). That keeps it
  a shared, project-wide secret, which is why rotation is left to the owner
  rather than run automatically.

## Design

### Two credentials, one role check

A project-scoped procedure now accepts either credential:

| Credential | Header | Role it carries |
|------------|--------|-----------------|
| Project secret key | `Authorization: API-Key <secretKey>` | `Admin` (`secretKeyRole`) |
| Session token + project name | `Authorization: Bearer <token>` and `x-yorkie-project: <name>` | the caller's own role in that project |

Both are checked against one table, `projectScopedMethods`
(`server/rpc/interceptors/admin.go`), which maps every project-scoped procedure
to the least role that may call it. Reads are `Member`; anything that changes
documents, schemas or sessions is `Admin`.

The table lists the project-scoped side on purpose: a procedure added later is
user-scoped until it is listed, so it cannot silently inherit a scheme whose
scope it does not read.

The secret key ranks as `Admin` rather than as the project's owner because only
`Admin` and above may read it (below), so it can carry no more authority than
the role that handed it out.

A `Bearer` token without `x-yorkie-project` is still refused on a project-scoped
procedure (`ErrSchemeNotAllowed`): it populates no project scope, and the
handler would read one that was never set. A project the caller cannot see and
one that does not exist answer alike (`ErrProjectAccessDenied`), so the header
cannot be used to probe project names. A role string the server does not
recognize fails closed, because `MemberRole.IsAtLeast` answers false whenever
either role is outside the `Owner`/`Admin`/`Member` hierarchy; the same holds
for a procedure missing from `projectScopedMethods`, whose required role is
then empty.

### Redaction

`projects.CanReadSecretKey` is the single rule — a role of at least `Admin`,
which an unrecognized role never is — and `server/projects` applies it where
the role is resolved, not in the handlers, so every present and future caller
is covered:

- `ProjectAndRole` clears the key when the resolved role is below `Admin`.
- `ListProjects` keeps it for owned projects (being in that list is the owner
  role) and reads the membership role back for member-sourced ones. A role that
  cannot be read is treated as no role: the key is redacted and the rest of the
  project still listed, so a membership revoked between the two queries does
  not fail the whole listing.

`CreateProject` (the caller is the owner), `UpdateProject` and
`RotateProjectKeys` (both already `CheckPermission(..., Admin)`) are unaffected.

### Client migration

This changes the wire contract for every client that authenticates
project-scoped calls with a key read from `GetProject`. Such a client must fall
back to its session when the key comes back empty:

```
if project.SecretKey != "" -> Authorization: API-Key <secretKey>
else                       -> Authorization: Bearer <session>
                              x-yorkie-project: <project.Name>
```

`admin.AuthInterceptor` (`admin/auth.go`) does this for the Go client. The
dashboard lives in a separate repository and needs the same change; until it
has one, a `member` signed into the dashboard sees its project pages fail,
because the key it used to receive is now empty. The header must also be
allowed through any proxy that filters request headers — the istio
`VirtualService` in `build/charts` lists it.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Non-Go clients break for `member` users the moment the server ships | Documented here and in the changelog as a breaking change; the Go client and the chart ship the fallback in the same change |
| A key copied before the change keeps full project authority | `RotateProjectKeys`; owners are told to run it after demoting or removing an admin |
| A demoted admin keeps the key | Same; automatic rotation was dropped because the key also signs the event webhook |
| A procedure added later reads a scope its scheme never set | `projectScopedMethods` is opt-in; an unlisted procedure is user-scoped |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Redact in `server/projects`, not in the handlers | The role is resolved there; a handler-level gate is one more thing each new caller must remember |
| Keep the protobuf field and empty the value | The generated `Project` message is shared with clients that still read it; removing the field is a wire break on top of a behavior break |
| The secret key ranks as `Admin`, not `Owner` | It is handed only to `Admin` and above, so it cannot carry more than that |
| `member` keeps the read procedures | Hiding the key alone would have taken `ListDocuments` from members too, which is not a privilege they were escalating |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Rotate the keys whenever a membership changes | The same key signs the event webhook, so rotation silently broke that integration |
| Give every member its own project-scoped key | A second credential type to issue, store and revoke, for what the session token already proves |
| Leave the key visible and gate only the destructive procedures | The key is the credential; anything holding it can call whatever the key is allowed to call, including procedures added later |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

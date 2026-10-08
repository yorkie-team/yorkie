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

## `connect.WithRecover` is a backstop, not the fix

The handler options are shared by the Yorkie, Admin and Cluster services, so
the recover covers all three. It turns a panic into `Internal` rather than a
`net/http` per-connection reset, which is strictly better for a caller behind a
proxy — but it reports a bug, it does not classify one. The interceptor check
is what makes the mismatched scheme a documented `PermissionDenied`.

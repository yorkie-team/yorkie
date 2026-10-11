**Created**: 2026-10-12

# Reject a keyless API-Key on the admin path

Fixes #2165 (and the closed duplicate #2161).

## Problem

`AdminServiceInterceptor.authenticate` rejects an empty `API-Key` param
only when `UseDefaultProject` is off. `UseDefaultProject` defaults to true,
and `projects.ProjectFromSecretKey(ctx, be, "")` returns the default project
without checking any credential. So on a stock deployment
`Authorization: API-Key ` (empty, ASCII whitespace, or U+00A0, which
net/http does not trim but `strings.TrimSpace` does) authenticates as the
default project for every project-scoped AdminService method:
`RemoveDocumentByAdmin`, `UpdateDocument`, `CreateSchema`, and so on.

The fallback came from #1475, which assumed admin callers would reach the
default project with an empty secret key. No caller does that today:

- `admin/auth.go` sends `project.SecretKey` from `GetProject`, which always
  carries the key (no redaction in the converter).
- The dashboard sets `API-Key` only when it holds a non-empty secret key.
- The JS SDK tests and prosemirror scripts read the key from the project.
- The MCP handler, which takes the same credential, already rejects an
  empty key.

The YorkieService (client) path uses the public key and stays as is: an
empty public key there is the intended single-tenant default-project path.

## Plan

- [x] Read the interceptor, the MCP handler, `ProjectFromSecretKey` and
      the history of the fallback (#1471, #1475); check the callers
- [x] RED: unit test of `authenticate` with a memory DB backend under
      `UseDefaultProject: true`; empty, whitespace-only and U+00A0-only
      keys on a project-scoped method must be rejected with
      `CodeUnauthenticated` + `ErrSecretKeyNotProvided`; a real secret key
      must still authenticate. Confirm the keyless cases fail on main
- [x] RED over HTTP: `TestRESTAPI/keyless_api_key_test` sends `API-Key`
      with empty, space and U+00A0 params to `ListDocuments`
- [x] Fix: in the API-Key branch, reject an empty-after-`TrimSpace` key
      regardless of `UseDefaultProject`
- [x] GREEN: the unit test passes
- [x] Run the related integration tests (admin / REST API / MCP)
- [x] `make verify`
- [x] Commit

## Review

- The API-Key branch of `authenticate` now trims the param once and rejects
  an empty result with `CodeUnauthenticated` + `ErrSecretKeyNotProvided`,
  whatever `UseDefaultProject` says. YorkieService is untouched.
- RED on main: the unit test authenticated all five keyless params under
  `UseDefaultProject=true`; the HTTP test got 200 for the three U+00A0
  params (empty and ASCII-space params were already 401, see lessons).
  GREEN after the fix.
- Integration tests touching AdminService (TestAdmin, TestAdminMember,
  TestAuthWebhook*, TestDocumentSchema, TestMCP, TestRESTAPI, TestUser,
  ...) pass; `make verify` passes.
- Follow-up from review: `projects.ProjectFromSecretKey` no longer falls
  back to the default project. An empty or whitespace-only key returns
  `projects.ErrSecretKeyNotProvided`, so any future caller fails closed.
  Its only callers are the admin interceptor and the MCP handler, both of
  which already reject an empty key; YorkieService uses
  `GetProjectFromAPIKey` (public key) and keeps its fallback. Unit test
  `TestProjectFromSecretKey`: on the old code the `""` case returned the
  default project. The `UseDefaultProject` comment in
  `server/backend/config.go` now says it never waives the secret key.

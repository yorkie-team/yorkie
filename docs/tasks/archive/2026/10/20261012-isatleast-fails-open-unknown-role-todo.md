**Created**: 2026-10-12

# Make `MemberRole.IsAtLeast` Fail Closed on an Unknown Role

**Goal:** Issue #2203. `MemberRole.IsAtLeast` ranks roles through a map
(`Owner:1, Admin:2, Member:3`) and returns `rank[r] <= rank[role]`. A receiver
the map does not know ranks 0, which compares as more privileged than Owner,
so `MemberRole("").IsAtLeast(Admin)` is true. #2196 guarded the one call site
it touched (`CanReadSecretKey`, and `authorizeProject` in the admin
interceptor) with `role.Validate()`, leaving the two authorization gates,
`authz.CheckPermission` and `authz.CheckPermissionByName`, unguarded: a
membership row whose `role` is empty or not one of the three constants passes
every permission check, including the Admin-gated `UpdateProject` and
`RotateProjectKeys`.

## Constraints

- The fix belongs in `IsAtLeast` itself, so every call site inherits it
  instead of the repo carrying guarded and unguarded ones.
- No behaviour change for the three known roles.
- An unknown *required* role must also fail closed, which is what the current
  code already does by accident (`rank[valid] <= 0` is false) and what
  `authorizeProject` relies on for a procedure missing from
  `projectScopedMethods`.

## Plan

- [x] RED: table test in `server/backend/database/member_info_test.go`
      covering the full known-role matrix plus `MemberRole("")`,
      `MemberRole("superuser")` as receiver and as required role.
- [x] `IsAtLeast` looks both roles up with the comma-ok form and returns
      false when either is unranked; the rank table moves to a package-level
      var so it is not rebuilt per comparison.
- [x] Drop the now-redundant `role.Validate()` guards in
      `projects.CanReadSecretKey` and `AdminServiceInterceptor.authorizeProject`,
      and rewrite the NOTE comments that described the fail-open behaviour,
      which would otherwise be stale.
- [x] `make lint`, `go test ./server/backend/database/... ./server/projects/...
      ./server/rpc/interceptors/...`.

## Review

- RED -> GREEN: `TestMemberRoleIsAtLeast` fails on the unknown-role rows
  before the change and passes after.
- Regression cover already in the tree: `server/projects/redact_test.go`
  (`MemberRole("")`, `MemberRole("superuser")` cannot read the secret key)
  and `TestIsSecretKeyAllowed` keep passing without their `Validate()`
  guards, which is the evidence the guards were redundant.

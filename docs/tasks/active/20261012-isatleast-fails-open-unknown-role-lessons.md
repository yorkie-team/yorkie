**Created**: 2026-10-12

# Make `MemberRole.IsAtLeast` Fail Closed on an Unknown Role — Lessons

Plan: `20261012-isatleast-fails-open-unknown-role-todo.md`.

## RED evidence

`TestMemberRoleIsAtLeast` with `IsAtLeast` restored to
`memberRoleRanks[r] <= memberRoleRanks[role]`:

```text
--- FAIL: TestMemberRoleIsAtLeast/""_is_at_least_"owner"_test
--- FAIL: TestMemberRoleIsAtLeast/""_is_at_least_"admin"_test
--- FAIL: TestMemberRoleIsAtLeast/""_is_at_least_"member"_test
--- FAIL: TestMemberRoleIsAtLeast/"superuser"_is_at_least_"owner"_test
--- FAIL: TestMemberRoleIsAtLeast/"superuser"_is_at_least_"admin"_test
--- FAIL: TestMemberRoleIsAtLeast/"superuser"_is_at_least_"member"_test
```

An unknown role was "at least Owner" — the exact answer `CheckPermission`
turns into a granted Admin operation. All rows pass after the change.

## Why the guard could not stay at the call sites

#2196 fixed the redaction gate by writing `role.Validate() == nil &&
role.IsAtLeast(...)`. That reads as a local hardening, but it is really a
workaround for a comparison that answers the wrong way: anyone adding a
fourth call site gets the fail-open answer unless they know to repeat the
guard, and nothing in the signature says so. Ranking an unknown role 0 only
looks safe because the map's numbers run the opposite way to the names —
`Owner` is 1 and the comparison is `<=`, so "unranked" lands *above* the top
of the hierarchy, not below it.

## The required role is checked too

`authorizeProject` does `required := projectScopedMethods[procedure]`, so a
procedure missing from the table yields `MemberRole("")` as the requirement.
Under the old code that denied by accident (`rank[owner]=1 <= 0` is false);
the new code denies deliberately. Keeping that direction matters: a method
added to the admin service but forgotten in the table is refused rather than
opened to every role.

## Scope

The two `Validate()` guards are removed rather than left as belt and braces,
because their NOTE comments assert the fail-open behaviour this change
deletes. A comment that describes a hazard which no longer exists is worse
than no comment: the next reader either repeats the guard or distrusts the
fix.

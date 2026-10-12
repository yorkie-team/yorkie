# Lessons: Redact project SecretKey from users below Admin

## The leak was in the layer that resolves the role, not the one that prints it

`converter.ToProject` has no idea who is asking, so a fix there could only be
unconditional, and the dashboard genuinely needs the key for owners and admins.
The role is known exactly twice: `ProjectAndRole` returns it, and
`ListProjects` can look it up per membership. Clearing the field at those two
points leaves the converter dumb and keeps the gate next to the authorization
decision it depends on.

## Redacting in `ProjectAndRole` is safe because no caller reads the key

All nine call sites in `server/rpc/admin_server.go` use the returned project
for `project.ID` or pass it to `documents.FindDocInfoByKey`. The only readers
of `Project.SecretKey` in the tree are `admin/auth.go` (client-side, from a
project the caller already holds) and `server/packs/pushpull.go` (project from
the API-Key context, not from this function). So the redaction is invisible to
every existing flow except the response bodies it is meant to change.

## `ListProjects` needs a per-project role lookup

`ListProjectInfosByMember` returns `ProjectInfo` values with the membership row
already dropped, so the role is not available to the caller. Re-reading it with
`FindMemberInfo` costs one query per member project, which is bounded by the
number of projects a user has been invited to. The owned list needs no lookup:
being in it *is* the owner role.

## Hiding the credential is not the same as checking the role

The first version of this change only hid the key, and recorded "members lose
the project-scoped procedures, read ones included" as the price. The review
panel upheld that finding twice. It was right: the procedures could not tell a
member from an owner because the only credential they accepted carried no
user, so hiding the key turned "every member acts as an owner" into "no member
acts at all". Neither is the intended permission model.

The fix was to give those procedures a credential that does carry a user: a
session token plus the project name in a header. The interceptor checks the
user's role against a per-method minimum, so a member reads and an admin
writes. The key keeps its meaning as the project's own full-authority
credential, which is exactly why only admins may read it.

Takeaway: when a security fix removes access, check whether the access was
wrong or only the credential was too coarse. Here the reads were legitimate;
the credential was the problem.

## Role changes are revocation only if the key follows them

The secret key names no user, so the role a user held when reading it is never
checked when the key is used. Hiding the key from members closed one way to get
it, but an admin who read it legitimately and was later demoted or removed kept
the project's whole authority. The review panel upheld this twice.

The key now follows the roles that may read it:

- `members.Remove` and `members.UpdateRole` replace the secret key when a user
  goes from a role that may read it (Admin and above) to one that may not, or
  leaves the project. `projects.CanReadSecretKey` is the single rule both this
  and the redaction use.
- Only the secret key is replaced (`projects.RotateSecretKey`). The public key
  identifies the project to SDK clients and carries no admin authority, so
  rotating it as `RotateProjectKeys` does would break every client for no
  security gain.
- The handlers broadcast a project cache invalidation, as `RotateProjectKeys`
  does, so the old key stops authenticating on other nodes too.
- The interceptor ranks the key as `Admin` against the per-method table, so the
  table covers both schemes. Today every entry is Member or Admin, so nothing
  changes; a method added later at Owner is refused to the key.

The cost is real and goes in the release note: demoting or removing an admin
replaces the key for every other holder too, including servers that call the
admin API with it or verify event webhook signatures with it. A member removed
leaves the key alone, since members can no longer read it.

A key read before this change by a user who is still below Admin is not
detected. Rotating the keys of projects that have ever had a member is the one
operator step on upgrade.

## `IsAtLeast` fails open, so the gate validates the role first

`MemberRole.IsAtLeast` compares ranks out of a map (`Owner:1, Admin:2,
Member:3`, `server/backend/database/member_info.go:56`), and a role missing
from that map yields the zero rank — which compares as *more* privileged than
Owner. An empty or corrupted role string would therefore have read as "at
least Admin" and kept the key. `redactForRole` calls `role.Validate()` before
the comparison so anything the hierarchy does not recognize is treated as least
privileged. The fix stays local to this gate: `IsAtLeast` is used elsewhere for
decisions with different failure modes, and changing its semantics is a
separate change.

## Review rounds

Not run: `/self-review` dispatches a reviewer subagent, and this autonomous run
is granted no tool that can launch one. The branch's review is CI, `@claude
review` and a human on the PR. Recording the skip rather than letting it read
as a clean round.

## The role read-back must not be able to fail the listing

`ListProjects` resolves each member project's role with a second query
(`FindMemberInfo`) because `ListProjectInfosByMember` drops the membership row.
That read can fail on its own: the member rows and the projects are fetched by
two separate queries, so a membership revoked in between leaves a project whose
row is already gone. Returning the error would turn a routine race into a failed
listing, which the call never did before the read-back existed. An unresolved
role is treated as no role instead — the key is withheld and the rest of the
project stays visible.

## A new request header needs the gateway's CORS list too

The Bearer + `x-yorkie-project` path adds a custom request header. The server's
own CORS allows every header (`server/rpc/server.go`), so local runs and the
integration suite pass, but the cluster chart enumerates the allowed headers
for the `/yorkie.v1` route
(`build/charts/yorkie-cluster/templates/istio/virtual-service.yaml`). Without
`x-yorkie-project` there, a browser's preflight fails and the dashboard never
reaches the new path. The header is now in that list. The `/auth` route's list
is left alone: no `/auth` handler reads the header.

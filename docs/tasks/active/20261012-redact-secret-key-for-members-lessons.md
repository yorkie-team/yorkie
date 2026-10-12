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

## A key a member already copied keeps working

Hiding the key stops a member from obtaining it now; it does not invalidate a
key read before this change. `RotateProjectKeys` is the remedy.

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

## Known limitation: Istio CORS allow-list

The Bearer + `x-yorkie-project` path adds a custom request header. The cluster
chart enumerates the allowed CORS headers for the `/yorkie.v1` route
(`build/charts/yorkie-cluster/templates/istio/virtual-service.yaml`), and
`x-yorkie-project` is not among them, so browser callers of that path are
blocked by preflight in Istio deployments. The chart is outside this change's
files; it needs a follow-up.

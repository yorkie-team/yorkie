---
title: auth-webhook-verb
target-version: 0.7.24
---

# Auth Webhook Verb

## Problem

The auth webhook gets one `attributes` entry for each resource a request
touches. For the four methods that send a change pack (`AttachDocument`,
`DetachDocument`, `RemoveDocument` and `PushPull`), `auth.AccessAttributes`
set the entry's `verb` from `pack.HasChanges()`:

```go
verb := types.Read
if pack.HasChanges() {
	verb = types.ReadWrite
}
```

This rule was written before presence moved into the document (#582). A
presence update has been a `Change` since then, so a pack that only moves a
cursor reported `rw`, the same as a pack that edits the root (#2104).

So a webhook could not enforce read-only members. The SDKs send the initial
presence with every attach and clear presence right before every detach,
which means an ordinary attach or detach always reported `rw`:

- If the webhook rejects `rw` for a read-only member, the member can neither
  open the document nor detach from it.
- If the webhook allows `rw` on `AttachDocument`, root operations made before
  the attach go through with the attach. `PushPull` never runs, so its role
  check is never reached.

The same rule got removal wrong the other way. `RemoveDocument` sends a pack
with `IsRemoved` set and usually no change at all, so a removal reported `r`,
and a webhook that rejects only `rw` let a read-only member remove the
document.

### Goals

- `verb` says whether a change-pack request writes the document, so the
  natural rule "reject `rw` from a read-only member" works on every method.

### Non-Goals

- Gating presence on its own. See Alternatives Considered.
- Server-side roles. The webhook stays the only place where a decision is made.

## Design

`auth.AccessAttributes(pack)` reports `rw` when the pack writes the document:

```go
verb := types.Read
if pack.OperationsLen() > 0 || pack.IsRemoved {
	verb = types.ReadWrite
}
```

| Pack | `verb` |
|------|--------|
| no changes | `r` |
| presence only (attach, detach, cursor move) | `r` |
| at least one operation | `rw` |
| removal requested by the client (`RemoveDocument`, `IsRemoved`), with or without changes | `rw` |

A pack reported as `r` can still carry presence changes, and the server
stores them like any other change. `r` means the document's content is not
changed, not that nothing is written.

Methods that do not send a change pack build their attributes with
`types.NewAccessAttributes` and are unaffected.

### Schema binding on attach

An attach can also write something the pack does not show: when no client is
attached, `AttachDocument` binds the schema the request names to the
document. The pack of such an attach is usually presence only, so it is
verified as `r`. Right before binding, if the request's schema key differs
from the document's, the handler asks the webhook again with `rw` for the
same document, so a read-only member cannot choose the schema that later
edits are checked against. It asks only when it is about to bind and the
first check was a read, so an ordinary attach still costs one webhook call.

On projects with an attachment limit or `RemoveOnDetach`, the bind runs for
any attach that finds no client attached, and it binds the requested key
even when that key is empty. So a read-only member that attaches a
schema-bound document without naming the schema, while no one else is
attached, is asked for `rw` and rejected. That rebinding predates this change
and is left as is.

The second webhook call is made while the document's attachment lock is
held, so on the bind path other attaches and detaches of that document wait
for the webhook.

### What `r` still allows

These follow from the attach and detach themselves, not from the pack, and
stay allowed for a member whose writes are rejected:

- Attaching to a key that does not exist yet creates an empty document, and
  the attach's `disable_presence` is fixed for it. Its content is not changed,
  and a webhook that authorizes per key decides which keys a member may open
  at all.
- With the project's `RemoveOnDetach` on, the last client's detach removes the
  document. The removal is the project's policy, set after the webhook was
  asked, so the detach is reported as `r`. Rejecting it would keep a read-only
  member from ever leaving.

### Decision cache

The decision cache is keyed by the marshaled request body
(`generateCacheKey`), and `verb` is part of the body. An answer cached for a
presence-only pack (`r`) is never reused for a pack that carries operations
(`rw`), and the other way around.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A webhook relied on presence reporting `rw` to block a member from showing presence | Such a rule could not be used: attach and detach always carry presence, so it blocked the member from opening the document at all, which rejecting the method does as well. Stated in the release notes. |
| A webhook allowed `r` on `RemoveDocument` for read-only members | That allowed them to remove documents, which this change stops. Stated in the release notes. |
| Webhook authors read `r` as "nothing is stored" | The security guide should state that `r` may carry presence changes, and what `r` still allows (above). The guide describes v0.7.23 today, so this is a follow-up for when this ships (yorkie-team.github.io#344). |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Change the meaning of `verb` rather than add a field (option A in #2104) | `verb` already exists to tell reads from writes, and the only behavior it loses is gating presence, which no webhook could use. A new field would leave every existing webhook as broken as before. |
| Count operations with `pack.OperationsLen()` | A presence-only change has no operations, so "has operations" is exactly "edits the root". The helper already exists. |
| Report removal as `rw` | Removing a document is a write. Without it, the new rule would keep reporting removals as reads. |
| Ask again with `rw` before binding a schema, instead of reporting every attach that names a schema as `rw` | Whether the attach binds is known only after the document is looked up, which happens after the first check. Reporting `rw` for any named schema would block read-only members from every document that uses schemas. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Keep `verb` and add `hasOperations` to change-pack attributes (option B in #2104) | Every webhook would have to learn a new field before read-only members work, and `verb` would keep a meaning that misleads. It also needs a `*bool` to tell "no operations" from "not a pack" or "an older server". |
| Strip presence from the pack before building the attributes | Same result for `verb`, but it hides that the rule is about operations. |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

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

The relaxation is scoped to the two methods that forced it. Attach and detach
carry presence the SDKs send on their own, with no way for the caller to leave
it out; `PushPull` and `RemoveDocument` carry the changes the caller chose to
send. So there are two rules, not one.

`auth.AccessAttributes(pack)` serves `PushPull` and `RemoveDocument`, where any
change is a write:

```go
verb := types.Read
if pack.HasChanges() || pack.IsRemoved {
	verb = types.ReadWrite
}
```

`auth.AttachmentAccessAttributes(pack)` serves `AttachDocument` and
`DetachDocument`, where presence alone is a read:

```go
verb := types.Read
if pack.OperationsLen() > 0 || pack.IsRemoved {
	verb = types.ReadWrite
}
```

| Pack | `AccessAttributes` | `AttachmentAccessAttributes` |
|------|--------------------|------------------------------|
| no changes | `r` | `r` |
| presence only | `rw` | `r` |
| at least one operation | `rw` | `rw` |
| removal (`IsRemoved`), with or without changes | `rw` | `rw` |

A pack reported as `r` on attach or detach can still carry presence changes,
and the server stores them like any other change: it writes a change document
and publishes it to every watcher. `r` there means the document's content is
not changed, not that nothing is written. Presence changed at any other point
in the attachment goes through `PushPull`, which reports `rw` for it, so a
webhook that rejects `rw` bounds a read-only member to the presence it sets at
attach and the clear it sends at detach.

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

### Removal on detach

With the project's `RemoveOnDetach` on, the detach of the last attached client
also removes the document, which the pack does not show either: the server sets
`IsRemoved` itself, after the webhook was asked with the pack's own verb. So
right before it does, if the detach was verified as `r`, `DetachDocument` asks
the webhook again with `rw` for the same document.

A refusal stops only the removal, not the detach: the client detaches and the
document stays, to be removed by the next detach that is allowed to remove it.
Refusing the detach instead would keep a read-only member from ever leaving,
and removing anyway would let it destroy the document on the way out. As on the
bind path, the second call is made only when the first check was a read, so an
ordinary detach of a writer still costs one webhook call. A webhook that is
unreachable fails the detach, the same as the first check does.

This check covers `YorkieService.DetachDocument` only. `RemoveOnDetach` also
removes the document on the deactivation path — `DeactivateClient` ->
`clients.Deactivate` -> `ClusterService.DetachDocument` — which performs the
same removal with no per-document webhook call, and is reached both by a client
closing itself and by housekeeping deactivating idle clients. Closing that path
needs the removal decision to travel from the handler that holds the caller's
token down to the cluster handler that does the removal, which housekeeping has
no token for at all. It is left as follow-up work.

### What `r` still allows

This follows from the attach itself, not from the pack, and stays allowed for a
member whose writes are rejected:

- Attaching to a key that does not exist yet creates an empty document, and
  the attach's `disable_presence` is fixed for it. Its content is not changed,
  and a webhook that authorizes per key decides which keys a member may open
  at all.

### Decision cache

The decision cache is keyed by the marshaled request body
(`generateCacheKey`), and `verb` is part of the body. An answer cached for a
presence-only pack (`r`) is never reused for a pack that carries operations
(`rw`), and the other way around.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A webhook relied on presence reporting `rw` to block a member from showing presence | Only the presence attach and detach carry stops reporting `rw`, and such a rule could not be used for it: attach and detach always carry presence, so it blocked the member from opening the document at all, which rejecting the method does as well. Presence sent through `PushPull` still reports `rw`. Stated in the release notes. |
| A webhook allowed `r` on `RemoveDocument` for read-only members | That allowed them to remove documents, which this change stops. Stated in the release notes. |
| Webhook authors read `r` as "nothing is stored" | The security guide should state that `r` may carry presence changes, and what `r` still allows (above). The guide describes v0.7.23 today, so this is a follow-up for when this ships (yorkie-team.github.io#344). |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Change the meaning of `verb` rather than add a field (option A in #2104) | `verb` already exists to tell reads from writes, and the only behavior it loses is gating presence, which no webhook could use. A new field would leave every existing webhook as broken as before. |
| Count operations with `pack.OperationsLen()` | A presence-only change has no operations, so "has operations" is exactly "edits the root". The helper already exists. |
| Relax presence to `r` only on attach and detach, not on every change-pack method | Presence is stored and broadcast, so it is a write the webhook has to be able to reject. Attach and detach are the only methods where rejecting it also rejects the thing a read-only member must be allowed to do, because the SDKs send presence there with no way to leave it out. On `PushPull` the caller chooses what to send, so there is nothing to relax. |
| Report removal as `rw` | Removing a document is a write. Without it, the new rule would keep reporting removals as reads. |
| Let a refused `RemoveOnDetach` removal fall back to a plain detach, instead of failing the detach | The member asked to leave, not to remove; failing the detach would strand a read-only member in the document, and removing anyway would let it destroy the document. |
| Ask again with `rw` before binding a schema, instead of reporting every attach that names a schema as `rw` | Whether the attach binds is known only after the document is looked up, which happens after the first check. Reporting `rw` for any named schema would block read-only members from every document that uses schemas. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Keep `verb` and add `hasOperations` to change-pack attributes (option B in #2104) | Every webhook would have to learn a new field before read-only members work, and `verb` would keep a meaning that misleads. It also needs a `*bool` to tell "no operations" from "not a pack" or "an older server". |
| Strip presence from the pack before building the attributes | Same result for `verb`, but it hides that the rule is about operations. |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

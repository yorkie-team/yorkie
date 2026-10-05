---
title: auth-webhook-presence-only
target-version: 0.7.24
---

# Auth Webhook Presence-Only Packs

## Problem

The auth webhook gets one `attributes` entry for each resource a request
touches. For the four methods that send a change pack (`AttachDocument`,
`DetachDocument`, `RemoveDocument` and `PushPull`), `auth.AccessAttributes`
sets the entry's `verb` from `pack.HasChanges()`:

```go
verb := types.Read
if pack.HasChanges() {
	verb = types.ReadWrite
}
```

This rule was written before presence moved into the document (#582). A
presence update has been a `Change` since then, so a pack that only moves a
cursor reports `rw`, the same as a pack that edits the root (#2104).

So a webhook cannot enforce read-only members. The SDKs send the initial
presence with every attach and clear presence right before every detach,
which means an ordinary attach or detach always reports `rw`:

- If the webhook rejects `rw` for a read-only member, the member can neither
  open the document nor detach from it.
- If the webhook allows `rw` on `AttachDocument`, root operations made before
  the attach go through with the attach. `PushPull` never runs, so its role
  check is never reached.

The same rule gets removal wrong the other way. `RemoveDocument` sends a pack
with `IsRemoved` set and usually no change at all, so a removal reports `r`,
and a webhook that rejects only `rw` lets a read-only member remove the
document.

### Goals

- A webhook can allow presence-only packs on every method and reject packs
  that edit or remove the document, including on `AttachDocument`.
- Webhooks that read only `verb` keep their decisions, except that a removal
  is now a write.

### Non-Goals

- Server-side roles. The webhook stays the only place where a decision is made.
- Authorizing the removal that the project's `RemoveOnDetach` makes on the
  last detach or deactivation. See "Removal on the last detach".

## Design

`types.AccessAttribute` gets one more field:

```go
type AccessAttribute struct {
	Key          string   `json:"key"`
	Verb         VerbType `json:"verb"`
	PresenceOnly *bool    `json:"presenceOnly,omitempty"`
}
```

`auth.AccessAttributes(pack)` sets both:

```go
verb := types.Read
if pack.HasChanges() || pack.IsRemoved {
	verb = types.ReadWrite
}
presenceOnly := pack.HasChanges() && pack.OperationsLen() == 0 && !pack.IsRemoved
```

| Pack | `verb` | `presenceOnly` |
|------|--------|----------------|
| no changes | `r` | `false` |
| presence only (attach, detach, cursor move) | `rw` | `true` |
| at least one operation | `rw` | `false` |
| removal (`IsRemoved`), with or without changes | `rw` | `false` |

`presenceOnly` is `true` only when the pack writes presence and nothing else.
A webhook that enforces read-only members allows an entry when `verb` is `r`
or `presenceOnly` is `true`, and rejects it otherwise.

The field describes the whole pack, not the change that triggered the request.
A client's pack carries every local change the server has not acknowledged,
so a detach can carry presence changes a rejected `PushPull` left pending. A
webhook that allows presence-only packs on some methods but not on others
accepts that presence on the method it allows. A webhook that reads
only `verb` sees the same verbs as before, except that a removal is now `rw`,
so it can still gate presence on its own by rejecting `rw`.

Methods that do not send a change pack build their attributes with
`types.NewAccessAttributes`. That leaves the field nil, so it is not in the
JSON at all. A webhook that wants to fail closed against an older server can
treat a missing `presenceOnly` on one of the four pack methods as `false`.

### Creating the document on attach

`AttachDocument` creates the document when it does not exist, and the create
fixates its `disable_presence`: the attach that inserts the row decides whether
the document carries presence at all. Neither write is in the pack, so an
attach approved as a read or as a presence write would otherwise let any member
create documents and pick that flag. Before the create, unless the attach was
already approved as a document write, the handler looks the document up and,
when it is not there, asks the webhook again for the same document with `rw`
and no `presenceOnly`. A read-only member can therefore attach to a document
that exists, but not bring one into being.

The lookup uses the predicate `FindOrCreateDocInfo` inserts on — the project's
key, not removed — so a document missing there is one that call would create.
It costs a read, so it is skipped when the project does not ask the webhook
about attaches: `VerifyAccess` would return without a call anyway.

### Schema binding on attach

An attach can also write something the pack does not show: when no client is
attached, `AttachDocument` binds the schema the request names to the
document. The pack of such an attach is presence only, or empty for a
presence-disabled document (`verb` `r`), so a webhook that allows those would
let any member choose the schema that later edits are checked against. Before
binding, unless the attach was already approved as a document write (`rw` and
not `presenceOnly`), the handler asks the webhook again for the same document
with `rw` and no `presenceOnly`, which such a webhook rejects for a read-only
member. An attach that binds nothing is asked once, as before.

Such an attach never rebinds without asking. The `DocInfo` the handler holds
was read before the document's attachment lock was taken, and the cached
`DocInfo` is not refreshed by a rebind, so the persisted binding may have
changed in between. Instead of comparing against that snapshot and then
writing over a binding it never saw, an attach that is not a document write
writes nothing when it names no schema or the schema it read, and is asked
for the write when it names another. So a read-only member attaching alone to
a document under its bound schema, which on a project with an attachment
limit or `RemoveOnDetach` reaches this path on every first attach, is not
asked again; and a concurrent rebind is never overwritten unchecked. An
attach already approved as a document write rebinds as before, the empty key
included.

The question is asked before the schema is looked up, so a rejected caller
does not learn whether the schema it named exists. The second call is made
while the document's attachment lock is held, so on the bind path other
attaches and detaches of that document wait for the webhook.

### Removal on the last detach

With the project's `RemoveOnDetach` on, the detach of the last attached client
also removes the document, and so does deactivating that client (through the
cluster `DetachDocument`). The server decides this after the webhook was asked
about the detach, so the webhook is never asked whether the member may remove
the document. That is intended: `RemoveOnDetach` is the project's policy that
a document no one is attached to is removed, and who left last does not
change that. The webhook decides whether a member may detach, not whether
the policy applies. So a read-only member whose presence-only detach is
allowed can be the one whose leaving removes the document.

### Read-only members with local edits

The SDKs do not know a member's role, so a client keeps a local edit made by a
read-only member and sends it with its next pack. `PushPull` and `DetachDocument`
then carry operations, so `presenceOnly` is false and the read-only rule
rejects them, the detach included. Applications should not let read-only
members edit; a member that does not edit always detaches with a
presence-only pack.

### Decision cache

The decision cache is keyed by the marshaled request body
(`generateCacheKey`), so `presenceOnly` is part of the key. An answer cached
for a presence-only pack is never reused for a pack that edits the document,
although both report `rw`.

`DropCachedDecisions` matches cached keys by the `"key":"<key>"` fragment of
the body. That fragment does not change, because `key` is still the first
field of each entry.

`watchStream.decisionKey`, which groups open Watch streams that ask the same
question, keys the attributes by their JSON too. Formatting them with `%v`
would print the `*bool` as an address.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A webhook reads a missing field as "presence only" against an older server | The field is present on every pack method, so a missing field there means an older server. Webhook authors should treat it as `false`. |
| A webhook allowed `r` removals | That let read-only members remove documents; a removal is now `rw`. Stated in the release notes. |
| Cached answers keyed by the old body shape | The keys change with the field. Entries in the old shape expire within `AuthWebhookCacheTTL` and are never matched again. |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Add a field and keep `verb` (option B in #2104) | Option A, reporting presence-only packs as `r`, was built first and reviewed. It moved real writes under `r`: presence is stored and broadcast, a `RemoveOnDetach` detach removes the document, and every fix for one path left another (deactivation) ungated. Keeping `verb` changes no existing decision except removal. |
| `presenceOnly`, not `hasOperations` | A rule "allow when there are no operations" would also allow a removal, which carries none. `presenceOnly` is false for a removal, so the read-only rule is a single check. |
| `*bool` with `omitempty` | Every pack method sends an explicit value, and every other method leaves the field out, so a webhook can tell "not presence only" from "not a pack" and from an older server. |
| Report removal as `rw` | Removing a document is a write. |
| Do not ask the webhook before a `RemoveOnDetach` removal | The removal is the project's policy for a document no one is attached to, not the member's request. Asking would also fail the detach of a read-only member who leaves last, and deactivation, which has no caller token, removes the document the same way. |
| Ask again before binding a schema | Whether the attach binds is known only after the document is looked up, which happens after the first check. Reporting every attach that names a schema as a write would block read-only members from every document that uses schemas. |
| Ask again before creating the document | Same reason: whether the attach creates is known only after the lookup. Reporting every attach as a write would block read-only members from every document, which is the problem this change set out to fix. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Report presence-only packs as `r` (option A in #2104) | See Design Decisions. Review showed the verb then no longer said whether a request writes. |
| Strip presence before building the attributes | Presence would never reach the webhook, so a deployment that wants to gate it could not. |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

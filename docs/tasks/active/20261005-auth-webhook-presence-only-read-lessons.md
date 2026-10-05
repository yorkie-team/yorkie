**Created**: 2026-10-05

# Auth Webhook: Presence-only Packs — Lessons

Plan: `20261005-auth-webhook-presence-only-read-todo.md`. Design:
`docs/design/auth-webhook-presence-only.md`.

## Outcome: option B, after option A failed review

The branch first shipped option A (presence-only packs report `r`) and went
through four rounds of the review panel, with fix rounds from the repo agent
in between. Each round found a write that A had moved under `r`:

1. A `RemoveOnDetach` detach removes the document, and the server sets
   `IsRemoved` after the webhook was asked. A removal recheck on the detach
   path was added.
2. Deactivating the client removes it the same way through the cluster
   `DetachDocument`, which asks no webhook and has no token to ask with. The
   recheck read as a guard but was bypassable.
3. Presence itself is stored and broadcast. Reporting it as `r` took away a
   webhook's only way to stop a member's presence on `PushPull`. The
   rebuttal ("no webhook could use that") was upheld against twice by the
   panel's adjudicator, which stopped the loop for a human.

The common cause is that A changed what an existing field means, and the
server performs writes the pack does not show. Option B keeps `verb` and adds
`presenceOnly`, so every existing decision stays as it was (removal aside),
and a webhook opts into allowing presence-only packs knowing what they can
lead to. The `RemoveOnDetach`/deactivation removal is then no worse than on
main and is left to a follow-up, stated in the design doc's Risks.

The final diff was rebuilt from the branch base rather than layered on the A
commits: the A-specific pieces (`AttachmentAccessAttributes`, the removal
recheck, the deferred-presence rule) are gone, and what carried over is the
removal-as-`rw` fix and the schema-binding recheck.

## `presenceOnly` rather than `hasOperations`

The issue suggested `hasOperations`. A read-only rule built on it ("allow
when there are no operations") also allows a removal, which carries no
operations. `presenceOnly` is false for a removal and for an empty pack, so
the rule is one check: allow when `verb` is `r` or `presenceOnly` is true.

## `*bool`, and the decision key that followed

The field is set on every pack method and omitted elsewhere, so a webhook can
tell "not presence only" from "not a pack" and from an older server. A
pointer field made `watchStream.decisionKey`, which formatted attributes with
`%v`, print an address, so it now keys them by JSON like the webhook cache;
a registry test with two equal pointers asserts one shared call.

## Proving each subtest

Each guard was disabled in turn and the integration test rerun:

- `presenceOnly` always false: the reader cannot attach, so the attach/detach,
  removal and schema subtests fail.
- Verb without `IsRemoved`: only the removal subtest fails (the removal goes
  through).
- No schema recheck: only the schema subtest fails (the bind goes through).

An earlier mutation that set the field to `nil` "passed" because the unused
variable broke the build and the grep for `--- FAIL` saw nothing; the build
error has to be checked too.

## Review of option B (before push)

An independent review found one blocking gap: the schema recheck first fired
only for a presence-only attach. A presence-disabled document attaches with an
empty pack (`verb` `r`, `presenceOnly` false), which the read-only rule
allows, and that attach bound the schema unchecked. Option A's recheck ("the
first check was a read") had covered it; narrowing it to `presenceOnly` lost
it. The recheck now fires unless the attach was already approved as a
document write (`auth.WritesDocument`), and the schema subtest attaches with
`WithDisablePresence` too; it fails with the narrower condition.

## `RemoveOnDetach` removal is policy, not a gap

Reviewers (the panel on option A, CodeRabbit on option B) kept raising that a
read-only member's last detach, or its deactivation, removes the document
under `RemoveOnDetach` without the webhook being asked. The author's call:
that is the policy working as designed. `RemoveOnDetach` removes a document
no one is attached to, whoever left last; the webhook decides whether a member
may detach. Gating it would fail a read-only member's detach when it leaves
last, and deactivation has no caller token to ask with. The design doc states
this under "Removal on the last detach", and no follow-up issue is filed.

## History (option A, superseded)

Everything below describes option A as it was built and reviewed. Its
conclusions, including the release note under "For the PR body", no longer
apply.


## Why option A after building option B

Option B (`hasOperations` next to an unchanged `verb`) was built first, for
backward compatibility. Weighing what A actually takes away changed the call:
A only removes the ability to gate presence as a write, and no webhook could
use that ability. Attach and detach always carry presence, so a rule that
rejects presence from a member already stops it from opening the document,
the same as rejecting the method. B kept every existing webhook as broken as
before until it learned a new field, and it needed a `*bool` (to tell "no
operations" from "not a pack" or "older server"), which in turn leaked into
`watchStream.decisionKey`'s `%v` formatting. A is one condition in
`auth.AccessAttributes`.

## Removal was reported as `r`

Building the new rule on operations alone would have kept a hole that exists
on main today. `RemoveDocument` sends a pack with `IsRemoved` and usually no
change (the Go and JS SDKs both set only the flag), so `HasChanges()` was
false and a removal reported `r`. A webhook that rejects `rw` from readers
let them remove documents. The rule is now `OperationsLen() > 0 ||
IsRemoved`. The integration subtest "a reader cannot remove the document"
fails without the `IsRemoved` term: the removal succeeds and the following
detach errors because the document is gone.

## Proving each subtest catches its case

- Old rule (`HasChanges()`): the attach/detach subtest fails (the reader is
  rejected), and so does the removal subtest, but only because its attach is
  rejected first. That does not prove the removal case.
- Operations only, without `IsRemoved`: only the removal subtest fails, now
  for the right reason.

## Self-review (round 1, correctness/tests)

An independent reviewer read the diff against the four handlers. Findings:

1. **Blocking, fixed: schema binding on attach went through as `r`.** With no
   client attached and a schema named, `AttachDocument` binds it
   (`UpdateDocInfoSchema`). The pack is presence only, so a read-only member
   could pick the schema later edits are checked against. Whether the attach
   binds is known only after the document lookup, which runs after the first
   check, so the handler now asks the webhook again with `rw` right before
   binding when the first check was a read. Reporting `rw` for any named
   schema instead would block readers from every schema document. Subtest "a
   reader cannot bind a schema but can attach under one" fails without the
   recheck (the bind succeeds).
2. **Blocking as a doc mismatch, documented: `RemoveOnDetach`.** The handler
   sets `IsRemoved` itself after the webhook check, so the last client's
   detach removes the document while reported as `r`. Kept: the removal is
   the project's policy, and rejecting the detach would keep a reader from
   ever leaving. The design doc's table now says the `rw` row is for removals
   the client requests, and a "What `r` still allows" section lists this and
   the empty document an attach creates for a new key.
3. **Non-blocking, fixed:** the design doc claimed the security guide already
   says `r` may carry presence. It does not yet; reworded as a follow-up.

Checked without findings: `Snapshot`/`VersionVector` in the request are not
written as content; nothing else depends on the old verb
(`HasChanges()` in `server/packs/pushpull.go` is unrelated); the Go client
always sends presence on attach and detach, so the subtests are meaningful;
each subtest uses its own key and the cache key includes the verb.

Noticed, out of scope: inside the `count == 0` branch, projects with an
attachment limit or `RemoveOnDetach` rebind the schema to whatever the
request names, including an empty key, even when the document already has
one. With this change a reader's attach there also needs `rw`.

## Self-review (round 2, design fit)

No blocking findings in the diff; the rounds stop here.

- The recheck runs under the same locks and leaves the same state on error as
  the `GetSchema` error just above it. The leftover attaching entry is not
  counted by `FindAttachedClientCount`, so it does not block a later bind.
- It uses `VerifyAccess` with `NewAccessAttributes`, the shape the channel
  handlers use, and the verb keeps the `r` and `rw` answers apart in the
  cache.
- Non-blocking, documented in the design doc: the recheck also fires for an
  empty schema key on attachment-limit / `RemoveOnDetach` projects (the
  rebinding noted above); the second webhook call holds the attachment lock;
  creating a key also fixes its `disable_presence`.

## For the PR body

- Release note: a presence-only pack now reports `verb: "r"`, and a removal
  reports `"rw"` even without changes.
- The security guide (yorkie-team.github.io#344) currently describes v0.7.23,
  where `rw` means "any change, presence included". It needs updating once
  this ships.

## Review round 3 (security)

Blocking, fixed: with the project's `RemoveOnDetach`, `DetachDocument` sets
`pack.IsRemoved` itself *after* the webhook was asked, so under the new rule
an ordinary presence-only detach was verified as `r` and still removed the
document — a read-only member could destroy it on the way out. Round 1 had
accepted this on the grounds that rejecting the detach would strand the
member; that framing missed a third option.

`canRemoveOnDetach` now asks the webhook once more with `rw` for the same
document, only when the first check was a read, right before the removal is
decided. A refusal stops the removal alone: the client detaches, the document
stays, and the next detach that is allowed to remove it does. So the reader
can always leave and cannot destroy data. Any other webhook error still fails
the detach, as the first check does.

`TestAuthWebhookRemoveOnDetachRead` covers it end to end: a reader detaching
last leaves the content intact, and the following writer detach removes it.

Not changed: presence-only packs are stored and broadcast under `r`. That is
the change's stated non-goal — the old rule could not gate presence either,
since attach and detach always carry it, so blocking presence meant blocking
the member from the document entirely. What is stored and fanned out, and the
size gate's treatment of operation-less changes, are the same before and
after this diff.

`server/rpc/cluster_server.go` runs the same `RemoveOnDetach` removal for the
server-internal detach of a deactivated client. It asks no webhook at all,
before or after this change, and is outside this diff.

## Review round 4 (panel)

Blocking, fixed: the relaxation was applied to every method that sends a
change pack, which made presence ungateable everywhere, not just where the
SDKs force it. Presence is not discarded — `packs.PushPull` stores it as a
change document and publishes it to every watcher, and the per-document size
gate lets an operation-less change through — so under the single rule a
read-only member could persist and broadcast arbitrary presence through
`PushPull` with `verb: r`.

The round-3 note above reasoned from attach and detach ("attach and detach
always carry it") and then generalized to all four methods. That step was
wrong: on `PushPull` the caller chooses what to send, so there is nothing the
webhook has to let through, and rejecting presence there costs a read-only
member nothing it must be able to do.

Split into two helpers. `AccessAttributes` keeps the old "any change is a
write" rule and serves `PushPull` and `RemoveDocument`; the new
`AttachmentAccessAttributes` applies the presence relaxation and serves
`AttachDocument` and `DetachDocument` only. A read-only member is now bounded
to the presence it sets at attach and the clear it sends at detach.

Lesson: when relaxing an authorization rule to unblock a caller, scope the
relaxation to the calls that are actually blocked. "The SDK always sends this"
is an argument about two methods; it does not carry to a method where the
caller picks the payload.

Not fixed, follow-up: the `RemoveOnDetach` removal on the deactivation path
(`DeactivateClient` -> `clients.Deactivate` ->
`ClusterService.DetachDocument`) still asks no webhook, so `canRemoveOnDetach`
guards only the client-facing detach. Closing it needs the removal decision to
travel from the handler holding the caller's token into the cluster handler,
and housekeeping deactivates idle clients with no token at all. Both files are
outside this diff. Recorded in the option A design doc, since replaced by `docs/design/auth-webhook-presence-only.md`.

## Review round 5 (panel)

Blocking, fixed: the relaxation could be used to defer a presence write past
the method that rejected it. `AttachmentAccessAttributes` keyed only on
`OperationsLen()`, but a pack is built from every unacknowledged local change
(`InternalDocument.CreateChangePack`), so a presence change a rejected
`PushPull` left pending was sent again with the next detach — and that pack,
presence-only, was reported as `r` and written. The round-4 integration test
walked exactly that sequence and asserted the detach succeeded.

The count is now part of the rule: a pack carrying more than one change is a
write however presence-only its changes are, because the relaxation only ever
covered the single presence change the SDKs send on their own (initial
presence at attach, clear at detach).

The cost is that a read-only member whose presence write was rejected cannot
detach either — its detach carries the rejected change. It still leaves by
deactivating, where the server builds the presence clear itself from the
client's checkpoint and the pending change is never sent, so "a read-only
member can always leave" holds. The alternative, having the handler drop the
deferred changes and detach anyway, would rewrite the client's pack and
checkpoint on its behalf; recorded in Alternatives Considered.

Lesson: an authorization rule over a change pack has to account for what the
pack may carry *next time*, not only what the current call intends. Client
changes that fail to sync are retained, so any method that flushes pending
changes inherits the verb of everything still pending.

Not fixed, follow-up (second round carrying it): the `RemoveOnDetach` removal
on the deactivation path is still ungated, and both `cluster_server.go` and
`server/clients` are outside this diff.

## Round 5 — schema snapshot and the field's promise

Blocking, fixed: the schema recheck decided on a stale snapshot. The guard was
`req.Msg.SchemaKey != docInfo.Schema`, but `docInfo` is read by
`FindOrCreateDocInfo` before the document's attachment locker is taken and is
never re-read under it, so a concurrent attach could rebind the schema in
between. A caller holding the older snapshot then saw "no change" and
`UpdateDocInfoSchema` overwrote the new binding with no `rw` check. Re-reading
under the lock would not have fixed it either: `UpdateDocInfoSchema` leaves the
`docCache` entry behind, so the re-read returns the same stale value.

The rule is now on the request alone, which no snapshot can skew: a caller
without a document write that names no schema skips the rebind entirely and
leaves the persisted binding standing; one that names a schema is asked for the
`rw`. A caller already approved as a document write behaves as before. The ask
also moved ahead of `schemas.GetSchema`, so a rejected caller cannot use the
error to learn whether the schema it named exists.

Lesson: when an authorization decision is a comparison against persisted state,
check where that state was read. A snapshot taken outside the lock that guards
the write cannot decide whether the write happens — phrase the rule over the
request, which is immutable, or hold the lock across both the read and the
write.

Also fixed: `PresenceOnly`'s doc comment read as a promise about the whole
request ("it does not remove the document"), while a `RemoveOnDetach` project
removes the document after a presence-only detach is allowed. The design doc
already recorded that removal as the project's policy; the comment now scopes
itself to the pack the client sent and names the `RemoveOnDetach` case, so a
webhook author is not told the request cannot end in a removal.

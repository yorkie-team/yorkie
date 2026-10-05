**Created**: 2026-10-05

# Auth Webhook: Presence-only Packs Report `r`

**Issue:** yorkie-team/yorkie#2104

**Goal:** Let an auth webhook enforce read-only members with the rule it would
write first, "reject `rw`": a presence-only attach and detach pass, and a pack
that edits or removes the document is rejected, including on
`AttachDocument`.

**Decision:** Option A from the issue. `verb` is `rw` only when the pack
carries operations or removes the document. Option B (a new `hasOperations`
field) was implemented first on `auth-webhook-has-operations` and dropped; see
the lessons file.

**Spec:** `docs/design/auth-webhook-verb.md`

## Tasks

- [x] `auth.AccessAttributes(pack)`: `rw` iff `pack.OperationsLen() > 0 ||
      pack.IsRemoved`.
- [x] `types.Read` / `types.ReadWrite` doc comments state the new meaning.
- [x] Unit tests: empty, presence-only, operations, operations behind
      presence, removal with and without changes; cache key differs between a
      presence-only and an operations pack.
- [x] Integration test: a webhook that rejects `rw` from a reader lets it
      attach and detach, rejects its pre-attach root edit and its removal, and
      sees a writer's edit as `rw`. Each failing subtest checked against the
      old rule and against the rule without `IsRemoved`.
- [x] `AttachDocument`: ask the webhook again with `rw` right before binding a
      schema when the first check was a read (self-review round 1).
- [x] Integration subtest: a reader cannot bind a schema on attach, but can
      attach under a schema a writer bound. Checked failing without the
      recheck.
- [x] Design doc; index it in `docs/design/README.md`. Covers schema binding,
      and what `r` still allows (creating an empty document on attach,
      `RemoveOnDetach` removal on the last detach).
- [x] `make verify`; `make test` with MongoDB up.
- [x] Self-review; log it in the lessons file.

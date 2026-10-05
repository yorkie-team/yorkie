**Created**: 2026-10-05

# Auth Webhook: Presence-only Packs

**Issue:** yorkie-team/yorkie#2104

**Goal:** Let an auth webhook allow presence-only change packs on every
method while rejecting packs that edit or remove the document, including on
`AttachDocument`.

**Decision:** Option B from the issue: keep `verb`, add `presenceOnly` to
change-pack attributes. Option A (presence-only packs report `r`) was
implemented first on this branch and replaced after review; see the lessons
file.

**Spec:** `docs/design/auth-webhook-presence-only.md`

## Tasks

- [x] `types.AccessAttribute.PresenceOnly *bool` (`presenceOnly`,
      omitempty), set by `auth.AccessAttributes(pack)` to
      `HasChanges() && OperationsLen() == 0 && !IsRemoved`.
- [x] `verb` is `rw` for a removal even without changes.
- [x] `AttachDocument`: before binding a schema, ask again with `rw` and no
      `presenceOnly` when the attach was presence only.
- [x] `watchStream.decisionKey` keys attributes by JSON.
- [x] Unit tests: verb and `presenceOnly` per pack shape, JSON shape, cache
      key separation, `DropCachedDecisions` needle, registry grouping.
- [x] Integration test with a webhook that allows `r` or `presenceOnly` for a
      reader: attach/presence/detach pass, a pre-attach edit, a removal and a
      schema bind are rejected, attaching under a bound schema passes, a
      writer's edit is not `presenceOnly`. Each guard checked by disabling it.
- [x] Design doc; index it in `docs/design/README.md`.
- [x] `make verify`; `make test` with MongoDB up.
- [ ] Follow-up issue: the `RemoveOnDetach` removal on the last detach or on
      deactivation is not authorized per document.

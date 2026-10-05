**Created**: 2026-10-05

# Reject Change Packs Naming Another Document

**Goal:** `PushPullChanges`, `DetachDocument` and `RemoveDocument` take the
document twice: as `DocumentId`, which the request is applied to, and as the
change pack's `DocumentKey`, which the auth webhook is asked about and the
document locks are taken on. Nothing checked that the two name the same
document. Reject a request where they differ.

## Tasks

- [x] `ErrDocumentKeyMismatch` (InvalidArgument) in `server/rpc`.
- [x] In the three handlers, compare `docInfo.Key` with `pack.DocumentKey`
      right after `FindDocInfoByRefKey`, before `packs.PushPull` writes.
- [x] Same check in the cluster service's `DetachDocument`, which pairs a
      document ID with a key too (review panel round 1).
- [x] Integration test with a raw RPC client: each handler rejects a pack that
      names another attached document, and the target stays `{}`. Checked
      failing without the check.
- [x] `make verify`; `make test` with MongoDB up.
- [x] Self-review; log it in the lessons file.

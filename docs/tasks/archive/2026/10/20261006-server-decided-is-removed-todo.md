**Created**: 2026-10-06

# Decide Document Removal by Method, Not by the Pack Flag

**Goal:** The server took `ChangePack.is_removed` from the client as is, on
every method that sends a pack. So `PushPullChanges`, `AttachDocument` and
`DetachDocument` could remove a document, bypassing a webhook that gates only
`RemoveDocument` (#2140), and a `RemoveDocument` sent with the flag unset and
no changes was asked as `r` while the handler still detached the client as
removed (#2134). Decide removal by the method: `RemoveDocument` always removes
and is asked as `rw`; the other methods never remove on the client's say.

## Tasks

- [x] Confirm both SDKs set `is_removed` only on `RemoveDocument` (Go
      `client.Remove`, JS `Client.remove`; every other pack comes from
      `createChangePack`, which sets it false). So overriding the flag on the
      other methods changes nothing for them, and is safer than rejecting
      for an unknown client.
- [x] `RemoveDocument`: set `pack.IsRemoved = true` right after decoding, so
      `auth.AccessAttributes` reports `rw` and `presenceOnly: false`, and
      `PushPull` marks the document removed.
- [x] `AttachDocument`, `DetachDocument`, `PushPullChanges`: set
      `pack.IsRemoved = false` right after decoding. `RemoveOnDetach` still
      sets it on the detach path after the webhook, unchanged.
- [x] Integration tests in `auth_webhook_test.go` with a raw RPC client on a
      project that gates only `RemoveDocument`: a pack flagged removed on
      attach, push-pull and detach removes nothing; a remove without the
      flag is asked as `rw`, rejected for a reader and removes for a writer.
      Checked failing without the fix.
- [x] Design doc `auth-webhook-presence-only.md`: removal is decided by the
      method.
- [x] `make verify`; targeted integration tests with MongoDB up.
- [x] Self-review; log it in the lessons file.

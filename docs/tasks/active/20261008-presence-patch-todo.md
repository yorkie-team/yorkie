**Created**: 2026-10-08

# Send presence as a patch on the way up (#2154)

Design: [presence-patch.md](../../design/presence-patch.md)

## Plan

- [x] Wire: `CHANGE_TYPE_PATCH` and `PresenceChange.removed_keys`, regenerated
      with the Makefile-pinned `buf` and plugins
- [x] Model: `presence.Patch`, `Change.RemovedKeys`, `Change.ApplyTo`, and
      `Execute` merging a patch into the stored presence
- [x] Converter: both directions, including a removal-only patch with no
      `presence`
- [x] Server: `Cache.PresenceBase` and `packs.foldPresencePatches` before
      `pushPack`, committed only after a successful push
- [x] `ErrPresenceBaseUnavailable` when a patch has no base
- [x] Advertise `presence-patch` in `ChangePack.capabilities` on every
      returned pack; drop the unhonoured `element-restore` from
      `ServerCapabilities`
- [x] Unit tests for the fold, the model and the converter; an integration
      test through attach and push-pull
- [ ] SDK: send patches when `presence-patch` is advertised, resend a full
      `PUT` on `ErrPresenceBaseUnavailable` (yorkie-js-sdk, separate PR)

- [x] Address the self-review: validate the base on checkpoint and epoch,
      commit after the pull from the stored changes, drop a patch a later
      put or clear replaces, `--presence-base-cache-size`, keep the fallback
      out of the error metric, refuse undefined presence change types

## Review

- Verified the integration test fails with the fold disabled, and the detach
  case fails without the rule that drops a replaced patch.
- `make verify` and the presence integration tests pass.

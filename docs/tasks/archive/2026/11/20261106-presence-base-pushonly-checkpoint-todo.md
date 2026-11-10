# Presence base keyed behind the stored checkpoint after a push-only reply

**Created**: 2026-11-06

Issue: [#2338](https://github.com/yorkie-team/yorkie/issues/2338)

## Problem

`presenceFold.commit` keyed the base to the in-memory checkpoint the reply
carries, while the next push looks it up under the stored one.
`UpdateClientInfoAfterPushPull` merges the checkpoint with `$max`, so the two
differ after a push-only reply, which returns the server seq the client sent.
A client that leaves a pull unapplied (the JS SDK during IME composition)
sends a server seq behind that pull's, and its next patch is refused with
`ErrPresenceBaseUnavailable` although the server holds its presence.

## Plan

- [x] Reproduce: pull past the client, push-only patch with the old server
      seq, next patch (`TestPresencePatch`, integration)
- [x] Read the client back after the pull and key the base to the stored
      checkpoint, the way the client cache is kept
- [x] Skip the read when there is nothing to commit
- [x] Document the stored-checkpoint keying in `commit` and
      `docs/design/presence-patch.md`
- [x] `make verify`, integration lane for `server/packs` and `server/rpc`

**Created**: 2026-10-12

# Omit minVersionVector from the reply to a push-only PushPull

Fixes #2097.

## Problem

`pullPack` in `server/packs/pushpull.go` sets
`resPack.VersionVector = minVersionVector` on every non-snapshot reply,
push-only replies included. A push-only reply carries no changes: the
client has not applied the remote changes that vector already accounts
for. A client that garbage-collects with it purges tombstones those
changes anchor on, and its first full pull afterwards fails on every
retry (`node not found` in Go, `cannot find node` in JS), because the
server redelivers the same pack.

Each SDK has had to drop the vector itself — JS
(yorkie-js-sdk#1393) and Go (#2096). Already released versions, and any
SDK that has not added the guard, stay exposed. Omitting it on the
server protects every client version at once; the client guards stay
useful against older servers.

The disable-GC path already omits the vector on change pulls, so the
wire shape (a reply with no `versionVector`) is one clients handle.

## Plan

- [x] Read `pullPack` / `preparePack` and the GC design doc; confirm the
      push-only reply has no snapshot branch (`preparePack` returns
      early for `SyncModePushOnly`)
- [x] Confirm no consumer reads a push-only reply's vector for anything
      but GC: the Go SDK's push-only path calls
      `Document.AcknowledgePushedChanges`, which reads only
      `Checkpoint.ClientSeq` and `IsRemoved`; the server's own push-only
      callers (`documents.UpdateDocument`, `revisions`, cluster detach)
      discard the returned pack entirely
- [x] Fix: keep calling `UpdateMinVersionVector`, but leave the reply's
      `VersionVector` unset when `opts.Mode == types.SyncModePushOnly`
- [x] Test: integration test in `server/packs/pushpull_test.go` — a
      push-only `PushPull` reply has no `VersionVector`, while the same
      client's push-pull reply still carries one, and the push-only call
      still advanced the stored min version vector
- [x] Update `docs/design/garbage-collection.md` ("GC Responsibility by
      Response Type" and the `minVersionVector` paragraph)
- [x] `make lint`, targeted `go test ./server/packs/...`
- [x] Commit and push

## Review

- `pullPack` now attaches `minVersionVector` only when the reply is
  neither a snapshot nor a push-only ack. `UpdateMinVersionVector` still
  runs for a push-only sync, so the client's own vector keeps
  contributing to the minimum and tombstones other clients still need
  are not collected early.
- Not architecture-changing: the GC responsibility split is already
  documented in `docs/design/garbage-collection.md`, which this updates
  rather than adding a new design doc.

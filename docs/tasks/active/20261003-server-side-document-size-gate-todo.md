**Created**: 2026-10-03

# Server-side gate for MaxSizePerDocument

## Problem

`MaxSizePerDocument` is a per-project quota
(`server/backend/database/project_info.go:135`, default 10 MiB) that only the
client enforces. The server sends it in the attach response
(`server/rpc/yorkie_server.go:360`), the SDK stores it on the document
(`client/client.go:603`), and `Document.Update` refuses the local update when
the clone's `DocSize.Total()` exceeds it (`pkg/document/document.go:307-312`).

The push path never re-reads it. `pushPack` filters already-pushed changes and
validates clientSeq continuity, serverSeq ordering and epoch
(`server/packs/pushpull.go:283-315`) before handing the remainder to
`CreateChangeInfos`; no branch there has a document, so none has a size. A
modified SDK or a direct Connect call can therefore grow a document past the
quota, bounded only by `maxRequestBytes` per push (`server/rpc/server.go:51`)
and, much later, MongoDB's 16 MiB record limit.

The gap is written up in `docs/design/document-size-limit.md`, which is a
proposal: it records the candidate gates and their costs but decides none of
them. This task owns the decision and the work, so the gap stops being
re-litigated in reviews of unrelated changes.

## Plan

- [ ] Decide the refusal semantics — the blocking question. `DocSize.Total()`
      is `Live + GC` (`pkg/document/resource/resource.go:20-28`), so deleting
      content moves bytes between the two without shrinking the total: a
      blanket refusal deadlocks a document that is already over quota, because
      the push that would delete content is refused for the reason the push
      that added it was. Pick among the design doc's options 1 (refuse growth
      only), 2 (refuse everything plus a defined recovery) and 3 (detach out
      of band), and write the decision back into the design doc.
- [ ] Decide where the number comes from. The lagging gate in the design doc
      persists `doc.Root().DocSize()` on `DocInfo` from `storeSnapshot` and
      reads it in `pushPack`, which already holds `currentDocInfo` under
      `DocPushKey`. Note the `docCache` hazard recorded in the doc's risk
      table before picking a source.
- [ ] Carry the new `DocInfo` field through both backends, `DocInfo.DeepCopy`
      and the snapshot write; zero means "unknown" and admits.
- [ ] Reset the field on every path that rebuilds or purges document
      internals — `CompactChangeInfos` in `mongo` and `memory` — or the gate
      refuses growth on a document compaction just shrank.
- [ ] Error code and SDK handling for whatever refusal semantic wins, in both
      `yorkie` and `yorkie-js-sdk`.
- [ ] Integration test pushing past the quota over a direct Connect call, not
      through the SDK's own gate, since the SDK's gate is what is being
      bypassed.
- [ ] Document the overshoot the lagging gate admits (up to one
      `SnapshotInterval` of pushes) as part of the quota's contract.

## Out of scope

- The client-side check. It stays: it is what gives an honest client a
  synchronous error at the edit that exceeds the quota.
- Byte-exact agreement between the server's number and the client's. Making
  the running `DocSize` accumulator agree with a rebuild is tracked by the
  rebuild-drift task and is a prerequisite, not part of this one.

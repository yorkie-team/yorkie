**Created**: 2026-05-27

# Disable GC on Attach Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-attachment `disableGC` flag so a client can opt out
of receiving the response VersionVector and participating in server-side
`minVV` tracking. Targets high-fan-out Counter/primitive workloads where
no client consumes tombstones.

**Architecture:** Add `bool disable_gc` to `AttachDocumentRequest`,
persist on `ClientDocInfo`, branch the `UpdateMinVersionVector` call in
`server/packs/pushpull.go`, surface the option on Go and JS SDK
`Attach`. Two PRs: Go server + Go SDK first, JS SDK after.

**Spec:** `docs/design/disable-gc-on-attach.md`

Branch: `disable-gc-on-attach`

---

## File Map

### Go (single PR: server + Go SDK)

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `api/yorkie/v1/yorkie.proto` | Add `bool disable_gc = 4` to `AttachDocumentRequest` |
| Modify | `api/yorkie/v1/yorkie.pb.go` | Regenerated via `make proto` |
| Modify | `server/backend/database/client_info.go` | Add `DisableGC bool` to `ClientDocInfo`; thread through `AttachDocument`/`DetachDocument` |
| Modify | `server/backend/database/mongo/client.go` | Persist `DisableGC` in attach update |
| Modify | `server/backend/database/memory/database.go` | Same for memory backend |
| Modify | `server/rpc/yorkie_server.go` | Pass `disable_gc` from request into `AttachDocument` |
| Modify | `server/packs/pushpull.go` | Guard `UpdateMinVersionVector` and `resPack.VersionVector` assignment on the flag |
| Modify | `client/client.go` | Add `WithDisableGC()` option; store on attached doc state |
| Modify | `client/attachment.go` (or equivalent) | Hold per-attachment `disableGC` flag |
| Modify | `client/client.go` PushPull path | Skip sending VV in request when `disableGC`; skip local GC when response VV empty |
| Create | `test/integration/disable_gc_test.go` | Integration coverage |
| Modify | `server/backend/database/testcases/testcases.go` | Cover `DisableGC` round-trip if existing table-driven tests touch `ClientDocInfo` |

### JS SDK (follow-up PR after Go merges)

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `packages/sdk/src/client/client.ts` | Accept `{ disableGC?: boolean }` on `attach` |
| Modify | `packages/sdk/src/document/document.ts` (or attachment store) | Hold per-attachment flag |
| Modify | `packages/sdk/src/api/converter.ts` | Set `disable_gc` on `AttachDocumentRequest`; skip VV serialization on push when set |
| Modify | `packages/sdk/src/document/document.ts` (sync path) | Skip GC when response VV is empty |
| Modify | `packages/sdk/test/integration/*` | Coverage mirroring the Go integration test |

---

## Task 1: Proto — add `disable_gc` to AttachDocumentRequest

**Files:**
- Modify: `api/yorkie/v1/yorkie.proto`

- [ ] **Step 1: Add the field**

In `AttachDocumentRequest`:

```protobuf
message AttachDocumentRequest {
  string client_id = 1;
  ChangePack change_pack = 2;
  string schema_key = 3;
  bool disable_gc = 4;  // NEW
}
```

- [ ] **Step 2: Regenerate**

Run: `make proto`
Expected: `api/yorkie/v1/yorkie.pb.go` updated; no other generated files
change unexpectedly.

- [ ] **Step 3: Commit**

```sh
git add api/yorkie/v1/yorkie.proto api/yorkie/v1/yorkie.pb.go
git commit -m "Add disable_gc field to AttachDocumentRequest"
```

---

## Task 2: Persist `DisableGC` on `ClientDocInfo`

**Files:**
- Modify: `server/backend/database/client_info.go`
- Modify: `server/backend/database/mongo/client.go`
- Modify: `server/backend/database/memory/database.go`

- [ ] **Step 1: Extend the struct**

In `client_info.go`:

```go
type ClientDocInfo struct {
    Status    string `bson:"status"`
    ServerSeq int64  `bson:"server_seq"`
    ClientSeq uint32 `bson:"client_seq"`
    Epoch     int64  `bson:"epoch"`
    DisableGC bool   `bson:"disable_gc,omitempty"`
}
```

- [ ] **Step 2: Thread through Attach/Detach**

Update `ClientInfo.AttachDocument(docID, alreadyAttached, epoch, disableGC bool)`
to set `DisableGC` on the new `ClientDocInfo`. Call sites must pass the
value through.

`DetachDocument(docID)` clears it to `false` alongside the other reset
fields so a re-attach is not contaminated by a stale flag.

- [ ] **Step 3: Backend writes**

In `mongo/client.go` and `memory/database.go`, ensure the
`UpdateClientInfoAfterPushPull` and any attach-time updates include the
new field. Because `omitempty` is set, no migration is needed; absent
values deserialize as `false`.

- [ ] **Step 4: Unit tests**

Add a table case (or extend the existing client_info tests) that
round-trips `DisableGC=true` through the chosen backend.

- [ ] **Step 5: Verify**

Run: `go test ./server/backend/database/...`
Expected: all PASS.

- [ ] **Step 6: Commit**

```sh
git add server/backend/database/
git commit -m "Persist DisableGC on ClientDocInfo for per-attachment opt-out"
```

---

## Task 3: RPC handler passes the flag

**Files:**
- Modify: `server/rpc/yorkie_server.go` (AttachDocument handler)

- [ ] **Step 1: Forward `request.DisableGc` into `AttachDocument`**

Where the handler currently calls `clientInfo.AttachDocument(...)`, pass
the new boolean. Keep the existing validation and authorization paths
unchanged.

- [ ] **Step 2: Verify**

Run: `go build ./...` and `go test ./server/rpc/...`
Expected: build OK; tests PASS.

- [ ] **Step 3: Commit**

```sh
git add server/rpc/yorkie_server.go
git commit -m "Forward disable_gc from AttachDocument RPC to ClientInfo"
```

---

## Task 4: Server-side branch in `pushpull.go`

**Files:**
- Modify: `server/packs/pushpull.go`

- [ ] **Step 1: Skip `UpdateMinVersionVector` for opt-out clients**

Around the existing call:

```go
disableGC := false
if doc := clientInfo.Documents[docInfo.ID]; doc != nil {
    disableGC = doc.DisableGC
}

if !disableGC {
    minVersionVector, err := be.DB.UpdateMinVersionVector(
        ctx, clientInfo, docInfo.RefKey(), reqPack.VersionVector,
    )
    if err != nil {
        return nil, err
    }
    if resPack.SnapshotLen() == 0 {
        resPack.VersionVector = minVersionVector
    }
}
// disableGC = true: resPack.VersionVector stays nil; converter
// serializes it as an empty VersionVector.
```

- [ ] **Step 2: Confirm `GetMinVersionVector` semantics unchanged**

`pullSnapshot`'s `GetMinVersionVector` call queries the
`versionvectors` collection directly. Since opt-out clients were never
inserted (Task 4 Step 1 skipped the write), the min is naturally
computed over participating clients only. No change needed here.

When all attached clients are opt-out, `GetMinVersionVector` returns an
empty vector and `GarbageCollect(emptyVV)` collects nothing — safe and
documented in the spec.

- [ ] **Step 3: Verify**

Run: `go test ./server/packs/...`
Expected: existing tests PASS; opt-out path is exercised by the
integration test in Task 6.

- [ ] **Step 4: Commit**

```sh
git add server/packs/pushpull.go
git commit -m "Skip minVV update and response VV for disable_gc clients"
```

Commit body should explain that the opt-out client neither writes to
`versionvectors` nor receives `minVV`, eliminating per-push cost in
GC-free workloads.

---

## Task 5: Go SDK `WithDisableGC` option

**Files:**
- Modify: `client/client.go`
- Modify: `client/attachment.go` (or wherever per-doc client state lives)

- [ ] **Step 1: Define the option**

```go
type attachOptions struct {
    // ... existing fields
    disableGC bool
}

// WithDisableGC declares that this attachment will not produce or consume
// tombstones. The server will skip minVV tracking and omit the response
// version vector for this client. Use only with Counter / primitive
// workloads; misuse leads to undefined GC behavior on this client.
func WithDisableGC() AttachOption {
    return func(o *attachOptions) { o.disableGC = true }
}
```

- [ ] **Step 2: Send in `AttachDocument` RPC**

Populate `request.DisableGc` from the option. Persist the flag on the
attached document's local state so subsequent PushPull paths can read it.

- [ ] **Step 3: Request-side VV omission**

When the attachment has `disableGC=true`, send an empty
`VersionVector` in `PushPull` (skip serialization). The client still
maintains its own per-change VV for causality but never uploads its
document-level VV.

- [ ] **Step 4: Response-side GC skip**

After receiving a `ChangePack`, if `VersionVector` is empty, skip the
local `GarbageCollect` call. The behavior is identical to receiving a
zero min VV today (nothing reclaimable) but avoids the traversal cost.

- [ ] **Step 5: Verify**

Run: `go test ./client/...`
Expected: existing tests PASS; integration test (Task 6) covers the
new path end-to-end.

- [ ] **Step 6: Commit**

```sh
git add client/
git commit -m "Add WithDisableGC attach option to Go client"
```

---

## Task 6: Go integration test

**Files:**
- Create: `test/integration/disable_gc_test.go`

- [ ] **Step 1: Write the test**

Build tag `integration`. Coverage:

1. **Response VV is empty** — client attaches with `WithDisableGC()`,
   makes a Counter increment, syncs; assert the received `ChangePack`
   has an empty `VersionVector`.
2. **`versionvectors` row is not written** — query the backend
   (memory or mongo) and confirm no row exists for the opt-out client
   on this document.
3. **Mixed-mode coexistence** — one client opt-out, one client opt-in
   on the same document, both performing Counter increments;
   opt-in client's `minVV` correctly reflects only its own VV (since
   it is the only participant).
4. **Re-attach clears the flag** — opt-out client detaches and
   re-attaches without the option; `DisableGC` is `false` after
   re-attach.

Follow patterns in `test/integration/counter_test.go` and
`test/integration/gc_test.go` for client setup and assertions.

- [ ] **Step 2: Verify**

Bring up the integration env:

```sh
docker compose -f build/docker/docker-compose.yml up --build -d
go test -tags integration ./test/integration/ -run TestDisableGC -v
```

Expected: all subtests PASS.

- [ ] **Step 3: Run the full integration suite**

```sh
make test
```

Expected: no regressions in existing tests (GC, snapshot, vv-cleanup).

- [ ] **Step 4: Commit**

```sh
git add test/integration/disable_gc_test.go
git commit -m "Add integration tests for disable_gc attach option"
```

---

## Task 7: Self-review and PR (Go)

- [ ] **Step 1: Lint and unit tests**

```sh
make lint
go test ./...
```

Expected: clean.

- [ ] **Step 2: Self-review**

Dispatch `superpowers:requesting-code-review` (or `/code-review`) over
the full branch diff. Apply Critical/Important findings; capture
non-blocking notes in the lessons file.

- [ ] **Step 3: Rebase on main and open PR**

```sh
git fetch && git rebase origin/main
git push -u origin disable-gc-on-attach
gh pr create --title "Disable GC on attach for Counter-only workloads" \
  --body-file <(cat <<'EOF'
## Summary
- Add `disable_gc` to `AttachDocumentRequest` and `ClientDocInfo`
- Skip `UpdateMinVersionVector` and response VV for opt-out clients
- Expose `WithDisableGC()` on the Go client

## Test plan
- [ ] `make lint`
- [ ] `go test ./...`
- [ ] `go test -tags integration ./test/integration/...`
- [ ] New `TestDisableGC` covers response VV, storage skip, mixed mode, re-attach
EOF
)
```

- [ ] **Step 4: Address review and merge**

Resolve CodeRabbit + maintainer comments; rebase if `main` moved.

---

## Task 8: JS SDK follow-up (separate PR after Go merges)

**Files:**
- Modify: `packages/sdk/src/client/client.ts`
- Modify: `packages/sdk/src/document/document.ts` (or attachment store)
- Modify: `packages/sdk/src/api/converter.ts`
- Add tests under `packages/sdk/test/integration/`

- [ ] **Step 1: API surface**

`client.attach(doc, { disableGC: true })`. Store the flag on the
attachment state.

- [ ] **Step 2: Converter**

`toAttachDocumentRequest` sets `disable_gc` from attachment state.
PushPull path uses empty VV when the flag is set.

- [ ] **Step 3: Sync path**

When the inbound `ChangePack` has an empty `VersionVector`, skip the
local GC traversal.

- [ ] **Step 4: Integration test**

Mirror the Go integration test against a running server built from the
Go PR. Assert response VV is empty and the doc replicates correctly
across two opt-out clients exchanging Counter increments.

- [ ] **Step 5: PR**

Title: `Add disableGC option to attach` (≤70 chars). Body includes
test plan and link to the Go PR.

---

## Task 9: Archive and lessons

- [ ] **Step 1: Capture lessons**

Write findings to `docs/tasks/active/20260527-disable-gc-on-attach-lessons.md`.
Likely topics:

- How `versionvectors` interacts with `omitempty` rows
- Anything surprising in the `pullSnapshot` GC path when all clients
  opt out
- Wire-compat behavior with mixed old/new client and server versions

- [ ] **Step 2: Archive**

```sh
bash scripts/tasks-archive.sh
bash scripts/tasks-index.sh
```

---

## Remaining

- [ ] All Task 1–9 steps above
- [ ] Schema integration (future, separate task): when schema lands,
      let schema declare `gc: false` to auto-apply `disable_gc` so
      client SDK does not need to opt in manually

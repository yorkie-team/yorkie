# Document Epoch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an epoch field to detect and recover clients stale after document compaction

**Architecture:** Add `Epoch` (int64) to `DocInfo` and `ClientDocInfo`. Increment on compaction, set on attach, compare before serverSeq validation in PushPull. Return `ErrEpochMismatch` so Go SDK can detach → re-attach.

**Tech Stack:** Go, MongoDB, go-memdb, protobuf, connect-go

---

**Created**: 2026-03-27

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `server/backend/database/doc_info.go` | Add `Epoch` to `DocInfo`, update `DeepCopy` |
| Modify | `server/backend/database/client_info.go` | Add `Epoch` to `ClientDocInfo`, set on attach |
| Modify | `server/packs/pushpull.go` | Add `ErrEpochMismatch`, epoch check in `preparePack` |
| Modify | `server/backend/database/mongo/client.go` | Increment epoch in `CompactChangeInfos`, propagate in `UpdateClientInfoAfterPushPull` |
| Modify | `server/backend/database/memory/database.go` | Increment epoch in memory `CompactChangeInfos` |
| Modify | `test/integration/compaction_test.go` | Test epoch mismatch detection and reattach recovery |

### Task 1: Add Epoch to DocInfo

**Files:**
- Modify: `server/backend/database/doc_info.go:27-60` (DocInfo struct)
- Modify: `server/backend/database/doc_info.go:74-92` (DeepCopy)

- [ ] **Step 1: Add Epoch field to DocInfo struct**

In `server/backend/database/doc_info.go`, add after the `CompactedAt` field:

```go
// Epoch is a monotonically increasing counter that increments on every
// compaction. A serverSeq is only meaningful within its epoch — once the
// epoch changes, all prior checkpoints are invalidated.
Epoch int64 `bson:"epoch"`
```

- [ ] **Step 2: Update DeepCopy to include Epoch**

In the `DeepCopy` method, add `Epoch: info.Epoch` to the returned struct literal.

- [ ] **Step 3: Verify build**

Run: `make build`
Expected: Build succeeds with no errors.

- [ ] **Step 4: Commit**

```bash
git add server/backend/database/doc_info.go
git commit -m "Add Epoch field to DocInfo for compaction generation tracking"
```

### Task 2: Add Epoch to ClientDocInfo and set on attach

**Files:**
- Modify: `server/backend/database/client_info.go:56-60` (ClientDocInfo struct)
- Modify: `server/backend/database/client_info.go:132-160` (AttachDocument method)

- [ ] **Step 1: Add Epoch field to ClientDocInfo**

In `server/backend/database/client_info.go`, add to the `ClientDocInfo` struct:

```go
type ClientDocInfo struct {
	Status    string `bson:"status"`
	ServerSeq int64  `bson:"server_seq"`
	ClientSeq uint32 `bson:"client_seq"`
	Epoch     int64  `bson:"epoch"`
}
```

- [ ] **Step 2: Update AttachDocument to accept and store epoch**

Change the `AttachDocument` signature to accept the document's epoch:

```go
func (i *ClientInfo) AttachDocument(docID types.ID, alreadyAttached bool, epoch int64) error {
```

Update the document initialization at line 152:

```go
i.Documents[docID] = &ClientDocInfo{
	Status:    DocumentAttached,
	ServerSeq: 0,
	ClientSeq: 0,
	Epoch:     epoch,
}
```

- [ ] **Step 3: Update all callers of AttachDocument**

Search for all call sites of `AttachDocument` and pass the document's epoch. The caller should have access to `docInfo.Epoch`. Use grep to find all callers:

Run: `grep -rn "AttachDocument(" --include="*.go" .`

Update each call site to pass `docInfo.Epoch` as the third argument.

- [ ] **Step 4: Verify build**

Run: `make build`
Expected: Build succeeds.

- [ ] **Step 5: Commit**

```bash
git add server/backend/database/client_info.go
# Also add any files where callers were updated
git commit -m "Add Epoch field to ClientDocInfo and set on attach"
```

### Task 3: Increment Epoch in CompactChangeInfos (MongoDB)

**Files:**
- Modify: `server/backend/database/mongo/client.go:1857-1868` (CompactChangeInfos update section)

- [ ] **Step 1: Add epoch increment to the document update**

In `server/backend/database/mongo/client.go`, in `CompactChangeInfos`, change the `$set` in the `UpdateOne` call from:

```go
"$set": bson.M{
	"server_seq":   newServerSeq,
	"compacted_at": gotime.Now(),
},
```

to:

```go
"$set": bson.M{
	"server_seq":   newServerSeq,
	"compacted_at": gotime.Now(),
},
"$inc": bson.M{
	"epoch": int64(1),
},
```

- [ ] **Step 2: Verify build**

Run: `make build`
Expected: Build succeeds.

- [ ] **Step 3: Commit**

```bash
git add server/backend/database/mongo/client.go
git commit -m "Increment epoch on compaction in MongoDB CompactChangeInfos"
```

### Task 4: Increment Epoch in CompactChangeInfos (Memory DB)

**Files:**
- Modify: `server/backend/database/memory/database.go:1843-1848` (CompactChangeInfos update section)

- [ ] **Step 1: Add epoch increment in memory DB**

In `server/backend/database/memory/database.go`, in `CompactChangeInfos`, add the epoch increment right before the `CompactedAt` assignment:

```go
loadedDocInfo.Epoch++
loadedDocInfo.CompactedAt = now
```

- [ ] **Step 2: Verify build**

Run: `make build`
Expected: Build succeeds.

- [ ] **Step 3: Commit**

```bash
git add server/backend/database/memory/database.go
git commit -m "Increment epoch on compaction in memory DB CompactChangeInfos"
```

### Task 4.5: Persist client epoch in UpdateClientInfoAfterPushPull

**Files:**
- Modify: `server/backend/database/mongo/client.go` (UpdateClientInfoAfterPushPull)
- Modify: `server/backend/database/memory/database.go` (UpdateClientInfoAfterPushPull)

- [x] **Step 1: Update MongoDB UpdateClientInfoAfterPushPull to persist epoch**

In the attached branch of `UpdateClientInfoAfterPushPull`, add epoch to the
`$set` clause so the client's epoch is written to MongoDB on every sync:

```go
clientDocInfoKey(docInfo.ID, "epoch"): clientDocInfo.Epoch,
```

Also add `existingDocInfo.Epoch == clientDocInfo.Epoch` to the cache bypass
condition so epoch changes are never skipped.

- [x] **Step 2: Update memory DB UpdateClientInfoAfterPushPull to persist epoch**

In the attached branch, include `Epoch: clientDocInfo.Epoch` in the
`ClientDocInfo` struct literal.

- [x] **Step 3: Update SystemClientInfo to include epoch**

In `server/backend/database/client_info.go`, add `Epoch: docInfo.Epoch` to
the synthetic `ClientDocInfo` in `SystemClientInfo` to prevent false mismatch
on server-side operations.

- [x] **Step 4: Verify build and commit**

Run: `make build`

```bash
git add server/backend/database/mongo/client.go \
       server/backend/database/memory/database.go \
       server/backend/database/client_info.go
git commit -m "Fix epoch propagation in cache bypass and SystemClientInfo"
```

> **Note:** This task was not in the original plan. It was discovered during
> Task 5 implementation: without persisting epoch in UpdateClientInfoAfterPushPull,
> a client that attaches to a compacted document (epoch=1) would have epoch=0
> in the DB after the first sync, causing a false ErrEpochMismatch on the next
> sync.

### Task 5: Add ErrEpochMismatch and epoch check in preparePack

**Files:**
- Modify: `server/packs/pushpull.go:66-70` (error definition)
- Modify: `server/packs/pushpull.go:274-324` (preparePack function)

- [ ] **Step 1: Write the failing integration test**

In `test/integration/compaction_test.go`, add a new test case inside `TestDocumentCompaction`:

```go
t.Run("epoch mismatch after force compaction triggers reattach", func(t *testing.T) {
	ctx := context.Background()

	d1 := document.New(helper.TestKey(t))
	assert.NoError(t, c1.Attach(ctx, d1, client.WithInitialRoot(
		yson.ParseObject(`{"text": Text()}`),
	)))
	assert.NoError(t, d1.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetText("text").Edit(0, 0, "hello")
		return nil
	}))
	assert.NoError(t, c1.Sync(ctx))

	// Force compact while client is attached
	assert.NoError(t, defaultServer.CompactDocument(ctx, d1.Key(), true))

	// Sync should fail with ErrEpochMismatch, not ErrInvalidServerSeq
	err := c1.Sync(ctx)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, packs.ErrEpochMismatch))
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags=integration ./test/integration/ -run TestDocumentCompaction/epoch_mismatch -count=1 -v`
Expected: FAIL — `ErrEpochMismatch` is not defined yet.

- [ ] **Step 3: Define ErrEpochMismatch**

In `server/packs/pushpull.go`, add to the `var` block:

```go
var (
	// ErrInvalidServerSeq is returned when the given server seq greater than
	// the initial server seq.
	ErrInvalidServerSeq = errors.Internal("invalid server seq").WithCode("ErrInvalidServerSeq")

	// ErrEpochMismatch is returned when the client's epoch does not match the
	// document's epoch. This happens after compaction resets the document — the
	// client must detach and re-attach to receive the compacted state.
	ErrEpochMismatch = errors.FailedPrecond("epoch mismatch").WithCode("ErrEpochMismatch")
)
```

- [ ] **Step 4: Add epoch check in preparePack**

In `preparePack`, add the epoch comparison **before** the existing serverSeq check (before line 294). The epoch values need to be passed into this function, so update the signature and the caller.

First, `preparePack` needs access to the client's epoch. The client epoch is available from `clientInfo.Documents[docInfo.ID].Epoch`. Add the check:

```go
// Compare epochs first: if the document has been compacted since the
// client last synced, serverSeq values from different epochs cannot
// be compared.
clientDocInfo := clientInfo.Documents[docInfo.ID]
if clientDocInfo != nil && clientDocInfo.Epoch != docInfo.Epoch {
	return nil, fmt.Errorf(
		"client epoch(%d) != document epoch(%d): %w",
		clientDocInfo.Epoch,
		docInfo.Epoch,
		ErrEpochMismatch,
	)
}
```

Place this between the push-only early return (line 292) and the serverSeq check (line 294).

- [ ] **Step 5: Verify the error propagates through connect/gRPC**

Check `server/rpc/connecthelper/errors.go` to confirm that `FailedPrecond` maps correctly. `errors.FailedPrecond` uses `ErrCodeFailedPrecondition = 9`, which maps to `connect.CodeFailedPrecondition`. No additional mapping needed.

- [ ] **Step 6: Run the integration test**

Run: `go test -tags=integration ./test/integration/ -run TestDocumentCompaction/epoch_mismatch -count=1 -v`
Expected: PASS

- [ ] **Step 7: Run all compaction tests to verify no regression**

Run: `go test -tags=integration ./test/integration/ -run TestDocumentCompaction -count=1 -v`
Expected: All tests pass. The existing "force compaction on attached document" test will now get `ErrEpochMismatch` instead of `ErrInvalidServerSeq` on `c1.Detach` — update the assertion in that test:

```go
// After force compaction, detach fails because epoch mismatch
err := c1.Detach(ctx, d1)
assert.Error(t, err)
```

- [ ] **Step 8: Commit**

```bash
git add server/packs/pushpull.go test/integration/compaction_test.go
git commit -m "Add ErrEpochMismatch and epoch check in preparePack"
```

### Task 6: Integration test for full reattach recovery flow

**Files:**
- Modify: `test/integration/compaction_test.go`

- [ ] **Step 1: Add reattach recovery test**

Add a test that verifies the complete self-healing flow — force compact, sync fails, detach (with new document instance), re-attach, sync succeeds:

```go
t.Run("client recovers from epoch mismatch by reattaching", func(t *testing.T) {
	ctx := context.Background()

	d1 := document.New(helper.TestKey(t))
	assert.NoError(t, c1.Attach(ctx, d1, client.WithInitialRoot(
		yson.ParseObject(`{"text": Text()}`),
	)))
	assert.NoError(t, d1.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetText("text").Edit(0, 0, "hello")
		return nil
	}))
	assert.NoError(t, c1.Sync(ctx))

	// Force compact
	assert.NoError(t, defaultServer.CompactDocument(ctx, d1.Key(), true))

	// Sync fails with epoch mismatch
	err := c1.Sync(ctx)
	assert.Error(t, err)

	// Recovery: create new document instance and re-attach
	d2 := document.New(d1.Key())
	assert.NoError(t, c1.Detach(ctx, d1, client.WithForce()))
	assert.NoError(t, c1.Attach(ctx, d2))

	// Verify the reattached document has the compacted content
	assert.Equal(t, `hello`, d2.Root().GetText("text").String())

	// Further edits work normally
	assert.NoError(t, d2.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetText("text").Edit(5, 5, " world")
		return nil
	}))
	assert.NoError(t, c1.Sync(ctx))
})
```

- [ ] **Step 2: Run the test**

Run: `make test -tags integration -run TestDocumentCompaction/client_recovers`
Expected: PASS

- [ ] **Step 3: Run full test suite**

Run: `go test -tags=integration ./test/integration/ -count=1 -v`
Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add test/integration/compaction_test.go
git commit -m "Add integration test for epoch mismatch reattach recovery"
```

### Task 7: Update design document

**Files:**
- Modify: `docs/design/document-epoch.md`

- [ ] **Step 1: Remove target-version placeholder if needed**

Verify the `target-version` in frontmatter matches the next release. Update if needed.

- [ ] **Step 2: Commit all remaining changes**

```bash
git add docs/design/document-epoch.md docs/design/README.md
git commit -m "Add document epoch design document"
```

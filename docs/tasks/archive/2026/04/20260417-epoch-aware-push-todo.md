**Created**: 2026-04-17

# Epoch-Aware Push Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add epoch check to `pushPack` so stale-epoch changes are discarded before being stored in DB/cache, preventing in-memory cache pollution after compaction.

**Architecture:** Add epoch comparison in `pushPack` before `CreateChangeInfos`. When epoch mismatches, discard pushable changes silently and let `preparePack` handle the client-facing error downstream.

**Tech Stack:** Go, integration tests

**Spec:** `docs/design/document-epoch.md` (updated "Epoch Check Placement" section)

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `server/packs/pushpull.go:175-234` | Add epoch check before `CreateChangeInfos` in `pushPack` |
| Modify | `test/integration/compaction_test.go` | Add test: stale-epoch push is discarded, cache stays clean |

---

### Task 1: Write Failing Integration Test

**Files:**
- Modify: `test/integration/compaction_test.go`

- [ ] **Step 1: Write the failing test**

Add a new subtest inside `TestDocumentCompaction` that verifies a stale-epoch client's push doesn't corrupt the document for other clients. Append before the closing `}` of `TestDocumentCompaction`:

```go
t.Run("stale epoch push does not corrupt document for other clients", func(t *testing.T) {
	ctx := context.Background()

	// 01. Client 1 creates a document with initial content.
	d1 := document.New(helper.TestKey(t))
	assert.NoError(t, c1.Attach(ctx, d1, client.WithInitialRoot(
		yson.ParseObject(`{"text": Text()}`),
	)))
	assert.NoError(t, d1.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetText("text").Edit(0, 0, "hello")
		return nil
	}))
	assert.NoError(t, c1.Sync(ctx))

	// 02. Client 2 attaches and syncs (pre-compaction epoch).
	d2 := document.New(d1.Key())
	assert.NoError(t, c2.Attach(ctx, d2))
	assert.NoError(t, c2.Sync(ctx))

	// 03. Force compact while both clients are attached.
	assert.NoError(t, defaultServer.CompactDocument(ctx, d1.Key(), true))

	// 04. Client 2 (stale epoch) makes a local change and tries to sync.
	// The push should be discarded; sync should fail with ErrEpochMismatch.
	assert.NoError(t, d2.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetText("text").Edit(5, 5, " world")
		return nil
	}))
	err := c2.Sync(ctx)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	assert.Equal(t, "ErrEpochMismatch", converter.ErrorCodeOf(err))

	// 05. Client 2 detaches (should succeed despite epoch mismatch).
	assert.NoError(t, c2.Detach(ctx, d2))

	// 06. Client 3 attaches fresh — this must succeed.
	// Before the fix, stale-epoch push from client 2 would pollute the
	// in-memory cache, causing "not applicable datatype" here.
	d3 := document.New(d1.Key())
	assert.NoError(t, c3.Attach(ctx, d3))
	assert.Equal(t, `hello`, d3.Root().GetText("text").String())

	// 07. Cleanup.
	assert.NoError(t, c1.Detach(ctx, d1))
	assert.NoError(t, c3.Detach(ctx, d3))
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd 03_projects/yorkie && go test -tags integration ./test/integration/ -run TestDocumentCompaction/stale_epoch_push -v -count=1`

Expected: The test should pass even without the fix in a single-server test setup (the cache pollution is timing-dependent). The test serves as a regression guard. Verify it compiles and runs.

- [ ] **Step 3: Commit**

```bash
git add test/integration/compaction_test.go
git commit -m "Add integration test for stale-epoch push discarding"
```

---

### Task 2: Add Epoch Check to pushPack

**Files:**
- Modify: `server/packs/pushpull.go:175-234`

- [ ] **Step 1: Add epoch check before CreateChangeInfos**

In `server/packs/pushpull.go`, modify the `pushPack` function. Insert the epoch check between the change filtering loop (line 202) and the `CreateChangeInfos` call (line 209).

Replace:

```go
	// 02. Push the changes to the database.
	if len(pushables) > 0 || reqPack.IsRemoved {
		locker := be.Lockers.Locker(DocPushKey(docKey))
		defer locker.Unlock()
	}
	docInfo, cpAfterPush, err := be.DB.CreateChangeInfos(
```

With:

```go
	// 02. Discard stale-epoch changes before storing them.
	// pushPack runs before preparePack. Without this check, stale-epoch
	// changes would be inserted into the in-memory changeCache, polluting
	// it with operations that reference pre-compaction CRDT node IDs.
	// preparePack will return ErrEpochMismatch to the client downstream.
	if len(pushables) > 0 {
		if clientDocInfo := clientInfo.Documents[docKey.DocID]; clientDocInfo != nil {
			docInfo, err := be.DB.FindDocInfoByRefKey(ctx, docKey)
			if err != nil {
				return nil, nil, time.InitialLamport, change.InitialCheckpoint, err
			}
			if clientDocInfo.Epoch != docInfo.Epoch {
				logging.From(ctx).Warnf(
					"discarding %d changes from stale epoch: client(%d) != doc(%d)",
					len(pushables),
					clientDocInfo.Epoch,
					docInfo.Epoch,
				)
				pushables = nil
			}
		}
	}

	// 03. Push the changes to the database.
	if len(pushables) > 0 || reqPack.IsRemoved {
		locker := be.Lockers.Locker(DocPushKey(docKey))
		defer locker.Unlock()
	}
	docInfo, cpAfterPush, err := be.DB.CreateChangeInfos(
```

Note: `FindDocInfoByRefKey` uses `docCache` (LRU) internally, so this read typically hits cache and adds negligible overhead.

- [ ] **Step 2: Fix the variable shadowing**

The code above introduces a `docInfo` variable inside the epoch-check block that shadows the outer `docInfo` returned by `CreateChangeInfos`. To fix, use a separate variable name for the epoch check:

Replace:

```go
			docInfo, err := be.DB.FindDocInfoByRefKey(ctx, docKey)
```

With:

```go
			currentDocInfo, err := be.DB.FindDocInfoByRefKey(ctx, docKey)
```

And update the epoch comparison to:

```go
			if clientDocInfo.Epoch != currentDocInfo.Epoch {
```

- [ ] **Step 3: Add logging import if not present**

Verify that `"github.com/yorkie-team/yorkie/server/logging"` is already imported in `pushpull.go`. It should be — `logging.From(ctx)` is used elsewhere in the file.

- [ ] **Step 4: Run existing tests**

Run: `cd 03_projects/yorkie && go test ./server/packs/ -v -count=1`
Expected: all existing unit tests PASS

- [ ] **Step 5: Run integration tests**

Run: `cd 03_projects/yorkie && go test -tags integration ./test/integration/ -run TestDocumentCompaction -v -count=1`
Expected: all 4 subtests PASS (including the new one from Task 1)

- [ ] **Step 6: Run lint**

Run: `cd 03_projects/yorkie && make lint`
Expected: no lint errors

- [ ] **Step 7: Commit**

```bash
git add server/packs/pushpull.go
git commit -m "Discard stale-epoch changes in pushPack before cache insertion

pushPack runs before preparePack in the PushPull flow. Without an
epoch check, stale-epoch changes are inserted into the in-memory
changeCache, polluting it with operations referencing pre-compaction
CRDT node IDs. Subsequent attach or compaction attempts replay these
cached operations against the post-compaction snapshot, causing
ErrNotApplicableDataType.

Add epoch comparison before CreateChangeInfos: if the client's epoch
does not match the document's epoch, discard the pushable changes.
preparePack still returns ErrEpochMismatch to the client downstream."
```

---

### Task 3: Update Design Document and Commit Together

**Files:**
- Verify: `docs/design/document-epoch.md` (already updated earlier in this session)

- [ ] **Step 1: Verify design doc changes**

Read `docs/design/document-epoch.md` and confirm the "Epoch Check Placement" section includes both `pushPack` and `preparePack` guards, and the "Risks and Mitigation" table includes the cache pollution row.

- [ ] **Step 2: Commit design doc**

```bash
git add docs/design/document-epoch.md
git commit -m "Update document-epoch design with pushPack epoch guard

Add pushPack as a second epoch check point to prevent stale-epoch
changes from polluting the in-memory changeCache. Document the
cache pollution risk and mitigation in the Risks table."
```

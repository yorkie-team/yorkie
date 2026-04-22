# VV Cleanup — Go Server + Go Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Created**: 2026-04-22

**Goal:** Remove detached clients' lamport entries from Version Vectors to stop VV from growing indefinitely.

**Architecture:** Server records DetachedLamport in existing ClientDocInfo on detach. On each PushPull, server checks if all attached clients have caught up (minVV >= detachedLamport), then signals removal via `detached_actors` in ChangePack. Clients augment change VV and minVV with detachedActors before execution and GC.

**Design doc:** `docs/design/vv-cleanup.md`

---

### Task 1: Proto Changes

**Files:**
- Modify: `api/yorkie/v1/resources.proto`

- [ ] **Step 1: Add detached_actors to ChangePack**

In `api/yorkie/v1/resources.proto`, add field 8 to ChangePack:

```protobuf
message ChangePack {
  string document_key = 1;
  Checkpoint checkpoint = 2;
  bytes snapshot = 3;
  repeated Change changes = 4;
  TimeTicket min_synced_ticket = 5; // deprecated
  bool is_removed = 6;
  VersionVector version_vector = 7;
  // detached_actors contains actors safe to remove from VV.
  // Server signals this when all clients have caught up.
  map<string, int64> detached_actors = 8;
}
```

- [ ] **Step 2: Add detached_actors to Snapshot**

In the same file, add field 3 to Snapshot:

```protobuf
message Snapshot {
  JSONElement root = 1;
  map<string, Presence> presences = 2;
  // detached_actors preserves removed actor lamports for VV
  // augmentation when restoring from snapshot.
  map<string, int64> detached_actors = 3;
}
```

- [ ] **Step 3: Regenerate protobuf code**

Run: `make proto`
Expected: Generated Go files updated in `api/yorkie/v1/`

- [ ] **Step 4: Verify build**

Run: `make build`
Expected: BUILD SUCCESS

- [ ] **Step 5: Commit**

```bash
git add api/yorkie/v1/
git commit -m "Add detached_actors field to ChangePack and Snapshot proto"
```

---

### Task 2: DB Schema — Add DetachedLamport to ClientDocInfo

**Files:**
- Modify: `server/backend/database/client_info.go`

- [ ] **Step 1: Add DetachedLamport field to ClientDocInfo**

In `server/backend/database/client_info.go`, update the struct at line 54:

```go
type ClientDocInfo struct {
	Status          string `bson:"status"`
	ServerSeq       int64  `bson:"server_seq"`
	ClientSeq       uint32 `bson:"client_seq"`
	Epoch           int64  `bson:"epoch"`
	DetachedLamport int64  `bson:"detached_lamport,omitempty"`
}
```

- [ ] **Step 2: Update DetachDocument to accept and store lamport**

Update the method at line 182:

```go
func (i *ClientInfo) DetachDocument(docID types.ID, detachedLamport int64) error {
	if err := i.EnsureDocumentAttachedOrAttaching(docID); err != nil {
		return err
	}

	i.Documents[docID].Status = DocumentDetached
	i.Documents[docID].ClientSeq = 0
	i.Documents[docID].ServerSeq = 0
	i.Documents[docID].DetachedLamport = detachedLamport
	i.UpdatedAt = gotime.Now()

	return nil
}
```

- [ ] **Step 3: Update DeepCopy to include DetachedLamport**

In the DeepCopy method at line 318, update the ClientDocInfo copy:

```go
documents[docID] = &ClientDocInfo{
	Status:          docInfo.Status,
	ServerSeq:       docInfo.ServerSeq,
	ClientSeq:       docInfo.ClientSeq,
	Epoch:           docInfo.Epoch,
	DetachedLamport: docInfo.DetachedLamport,
}
```

- [ ] **Step 4: Fix all callers of DetachDocument**

Search all call sites of `DetachDocument(` and update to pass the lamport value.
The callers need to pass the client's lamport at detach time. Each call site
should have access to the version vector or lamport from the change pack.

Key call sites to update:
- `server/rpc/yorkie_server.go` — DetachDocument RPC handler
- `server/rpc/cluster_server.go` — Cluster DetachDocument handler
- `server/packs/pushpull.go` — if detach is handled inline

For each call site, extract the lamport from the client's version vector:
```go
// Example: get max lamport from the request's version vector
clientInfo.DetachDocument(docID, reqPack.VersionVector.MaxLamport())
```

- [ ] **Step 5: Verify build compiles**

Run: `make build`
Expected: BUILD SUCCESS

- [ ] **Step 6: Run unit tests**

Run: `make test`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add server/backend/database/client_info.go
# Also add any files with updated DetachDocument call sites
git commit -m "Add DetachedLamport field to ClientDocInfo"
```

---

### Task 3: Database Interface — Add FindDetachedClients Method

**Files:**
- Modify: `server/backend/database/database.go`
- Modify: `server/backend/database/mongo/client.go`
- Modify: `server/backend/database/memory/database.go`

- [ ] **Step 1: Define DetachedClientInfo struct**

In `server/backend/database/client_info.go`, add:

```go
// DetachedClientInfo contains information about a detached client for VV cleanup.
type DetachedClientInfo struct {
	ActorID         time.ActorID
	DetachedLamport int64
}
```

- [ ] **Step 2: Add FindDetachedClients to Database interface**

In `server/backend/database/database.go`, add to the interface:

```go
// FindDetachedClients returns detached clients with non-zero DetachedLamport
// for the given document.
FindDetachedClients(
	ctx context.Context,
	docRefKey types.DocRefKey,
) ([]DetachedClientInfo, error)
```

- [ ] **Step 3: Add ResetDetachedLamport to Database interface**

In the same interface, add:

```go
// ResetDetachedLamport resets the DetachedLamport of the given client
// after all attached clients have been notified.
ResetDetachedLamport(
	ctx context.Context,
	clientID types.ID,
	docID types.ID,
) error
```

- [ ] **Step 4: Implement FindDetachedClients in MongoDB**

In `server/backend/database/mongo/client.go`:

```go
func (c *Client) FindDetachedClients(
	ctx context.Context,
	docRefKey types.DocRefKey,
) ([]database.DetachedClientInfo, error) {
	filter := bson.M{
		"project_id": docRefKey.ProjectID,
		fmt.Sprintf("documents.%s.status", docRefKey.DocID): database.DocumentDetached,
		fmt.Sprintf("documents.%s.detached_lamport", docRefKey.DocID): bson.M{"$gt": int64(0)},
	}

	cursor, err := c.collection(ColClients).Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("find detached clients: %w", err)
	}
	defer func() { _ = cursor.Close(ctx) }()

	var results []database.DetachedClientInfo
	for cursor.Next(ctx) {
		var info database.ClientInfo
		if err := cursor.Decode(&info); err != nil {
			return nil, fmt.Errorf("decode client info: %w", err)
		}
		results = append(results, database.DetachedClientInfo{
			ActorID:         info.Key,
			DetachedLamport: info.Documents[docRefKey.DocID].DetachedLamport,
		})
	}

	return results, nil
}
```

NOTE: `ActorID` should be derived from the client's actor ID, not `Key`. Check how
the codebase maps ClientInfo to ActorID — it may require looking up the client's
actor from the version vector or storing it separately.

- [ ] **Step 5: Implement FindDetachedClients in Memory DB**

In `server/backend/database/memory/database.go`:

```go
func (d *DB) FindDetachedClients(
	ctx context.Context,
	docRefKey types.DocRefKey,
) ([]database.DetachedClientInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iterator, err := txn.Get(tblClients, "project_id", docRefKey.ProjectID.String())
	if err != nil {
		return nil, fmt.Errorf("find detached clients: %w", err)
	}

	var results []database.DetachedClientInfo
	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.ClientInfo)
		docInfo, ok := info.Documents[docRefKey.DocID]
		if !ok {
			continue
		}
		if docInfo.Status == database.DocumentDetached && docInfo.DetachedLamport > 0 {
			results = append(results, database.DetachedClientInfo{
				ActorID:         info.Key,
				DetachedLamport: docInfo.DetachedLamport,
			})
		}
	}

	return results, nil
}
```

- [ ] **Step 6: Implement ResetDetachedLamport in both DB backends**

MongoDB:
```go
func (c *Client) ResetDetachedLamport(
	ctx context.Context,
	clientID types.ID,
	docID types.ID,
) error {
	_, err := c.collection(ColClients).UpdateOne(ctx, bson.M{
		"_id": clientID,
	}, bson.M{
		"$set": bson.M{
			clientDocInfoKey(docID, "detached_lamport"): int64(0),
		},
	})
	return err
}
```

Memory DB: find the client by ID and set `DetachedLamport = 0`.

- [ ] **Step 7: Verify build and tests**

Run: `make build && make test`
Expected: BUILD SUCCESS, tests PASS

- [ ] **Step 8: Commit**

```bash
git add server/backend/database/
git commit -m "Add FindDetachedClients and ResetDetachedLamport to database layer"
```

---

### Task 4: Server Logic — Signal detached_actors in PushPull Response

**Files:**
- Modify: `server/packs/pushpull.go`
- Modify: `server/packs/serverpack.go`

- [ ] **Step 1: Add DetachedActors to ServerPack**

In `server/packs/serverpack.go`, add field to the struct:

```go
type ServerPack struct {
	DocumentKey   key.Key
	Checkpoint    change.Checkpoint
	ChangeInfos   []*database.ChangeInfo
	Snapshot      []byte
	VersionVector time.VersionVector
	IsRemoved     bool
	DetachedActors map[string]int64  // NEW: actorID → lamport
}
```

- [ ] **Step 2: Update ToPBChangePack to include DetachedActors**

In the `ToPBChangePack` method of ServerPack, add `DetachedActors` to the
protobuf conversion:

```go
pbPack.DetachedActors = p.DetachedActors
```

- [ ] **Step 3: Add detached_actors logic in pullPack**

In `server/packs/pushpull.go`, in the `pullPack` function, after computing
`minVersionVector` (around line 303), add the detached actors check:

```go
// 04. Find detached clients whose lamport is covered by minVV.
detachedClients, err := be.DB.FindDetachedClients(ctx, docInfo.RefKey())
if err != nil {
	return nil, err
}

detachedActors := make(map[string]int64)
for _, dc := range detachedClients {
	if l, ok := minVersionVector.Get(dc.ActorID); ok && l >= dc.DetachedLamport {
		detachedActors[dc.ActorID.String()] = dc.DetachedLamport
	}
}

if len(detachedActors) > 0 {
	resPack.DetachedActors = detachedActors
}
```

NOTE: After signaling, the server should eventually reset DetachedLamport.
However, the reset should happen only after all clients receive the signal.
A simpler approach: reset after the first signal, since the condition
`minVV >= detachedLamport` already guarantees all clients have caught up.
The server can reset immediately after including in the response:

```go
for _, dc := range detachedClients {
	if l, ok := minVersionVector.Get(dc.ActorID); ok && l >= dc.DetachedLamport {
		detachedActors[dc.ActorID.String()] = dc.DetachedLamport
		// Reset since all clients are already caught up
		if err := be.DB.ResetDetachedLamport(ctx, dc.ClientID, docInfo.ID); err != nil {
			return nil, err
		}
	}
}
```

IMPORTANT: The DetachedClientInfo struct may need a `ClientID` field for the
reset. Adjust the struct and queries accordingly.

- [ ] **Step 4: Verify build**

Run: `make build`
Expected: BUILD SUCCESS

- [ ] **Step 5: Commit**

```bash
git add server/packs/
git commit -m "Signal detached_actors in PushPull response when minVV covers detach lamport"
```

---

### Task 5: Converter — Serialize/Deserialize detached_actors

**Files:**
- Modify: `api/converter/to_pb.go`
- Modify: `api/converter/from_pb.go`
- Modify: `api/converter/to_bytes.go`
- Modify: `api/converter/from_bytes.go`
- Modify: `pkg/document/change/pack.go`

- [ ] **Step 1: Add DetachedActors to change.Pack**

In `pkg/document/change/pack.go`, add to the Pack struct:

```go
type Pack struct {
	DocumentKey   key.Key
	Checkpoint    Checkpoint
	Changes       []*Change
	Snapshot      []byte
	VersionVector time.VersionVector
	IsRemoved     bool
	DetachedActors map[time.ActorID]int64  // NEW
}
```

- [ ] **Step 2: Update ToChangePack converter**

In `api/converter/to_pb.go`, update `ToChangePack`:

```go
func ToChangePack(pack *change.Pack) (*api.ChangePack, error) {
	pbChanges, err := ToChanges(pack.Changes)
	if err != nil {
		return nil, err
	}

	pbVersionVector, err := ToVersionVector(pack.VersionVector)
	if err != nil {
		return nil, err
	}

	pbDetachedActors := make(map[string]int64)
	for actorID, lamport := range pack.DetachedActors {
		pbDetachedActors[actorID.String()] = lamport
	}

	return &api.ChangePack{
		DocumentKey:    pack.DocumentKey.String(),
		Checkpoint:     ToCheckpoint(pack.Checkpoint),
		Changes:        pbChanges,
		Snapshot:       pack.Snapshot,
		VersionVector:  pbVersionVector,
		IsRemoved:      pack.IsRemoved,
		DetachedActors: pbDetachedActors,
	}, nil
}
```

- [ ] **Step 3: Update FromChangePack converter**

In `api/converter/from_pb.go`, update `FromChangePack`:

```go
// After existing parsing, add:
detachedActors := make(map[time.ActorID]int64)
for actorIDStr, lamport := range pbPack.DetachedActors {
	actorID, err := time.ActorIDFromHex(actorIDStr)
	if err != nil {
		return nil, err
	}
	detachedActors[actorID] = lamport
}

pack := &change.Pack{
	DocumentKey:    key.Key(pbPack.DocumentKey),
	Checkpoint:     fromCheckpoint(pbPack.Checkpoint),
	Changes:        changes,
	Snapshot:       pbPack.Snapshot,
	IsRemoved:      pbPack.IsRemoved,
	VersionVector:  versionVector,
	DetachedActors: detachedActors,
}
```

- [ ] **Step 4: Update SnapshotToBytes to include detached_actors**

In `api/converter/to_bytes.go`, update `SnapshotToBytes` signature and body:

```go
func SnapshotToBytes(
	obj *crdt.Object,
	presences map[string]presence.Data,
	detachedActors map[time.ActorID]int64,
) ([]byte, error) {
	pbElem, err := toJSONElement(obj)
	if err != nil {
		return nil, err
	}

	pbPresences := ToPresences(presences)

	pbDetachedActors := make(map[string]int64)
	for actorID, lamport := range detachedActors {
		pbDetachedActors[actorID.String()] = lamport
	}

	bytes, err := proto.Marshal(&api.Snapshot{
		Root:           pbElem,
		Presences:      pbPresences,
		DetachedActors: pbDetachedActors,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal Snapshot to bytes: %w", err)
	}

	return bytes, nil
}
```

- [ ] **Step 5: Update BytesToSnapshot to return detached_actors**

In `api/converter/from_bytes.go`, update `BytesToSnapshot`:

```go
func BytesToSnapshot(snapshot []byte) (*crdt.Object, *presence.Map, map[time.ActorID]int64, error) {
	if len(snapshot) == 0 {
		return crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket), presence.NewMap(), nil, nil
	}

	pbSnapshot := &api.Snapshot{}
	if err := proto.Unmarshal(snapshot, pbSnapshot); err != nil {
		return nil, nil, nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	obj, err := fromJSONElement(pbSnapshot.GetRoot())
	if err != nil {
		return nil, nil, nil, err
	}

	presences := fromPresences(pbSnapshot.GetPresences())

	detachedActors := make(map[time.ActorID]int64)
	for actorIDStr, lamport := range pbSnapshot.GetDetachedActors() {
		actorID, err := time.ActorIDFromHex(actorIDStr)
		if err != nil {
			return nil, nil, nil, err
		}
		detachedActors[actorID] = lamport
	}

	return obj.(*crdt.Object), presences, detachedActors, nil
}
```

- [ ] **Step 6: Fix all callers of SnapshotToBytes and BytesToSnapshot**

Search for all call sites and update signatures. Key locations:
- `server/packs/snapshots.go` or wherever snapshots are created
- `pkg/document/internal_document.go:applySnapshot`

- [ ] **Step 7: Verify build and tests**

Run: `make build && make test`
Expected: BUILD SUCCESS, PASS

- [ ] **Step 8: Commit**

```bash
git add api/converter/ pkg/document/change/pack.go
git commit -m "Add detached_actors serialization to ChangePack and Snapshot converters"
```

---

### Task 6: Go Client — Document-Level detachedActors

**Files:**
- Modify: `pkg/document/internal_document.go`
- Modify: `pkg/document/document.go`

- [ ] **Step 1: Add detachedActors map to InternalDocument**

In `pkg/document/internal_document.go`, add field to the struct:

```go
type InternalDocument struct {
	key            key.Key
	status         StatusType
	checkpoint     change.Checkpoint
	changeID       change.ID
	root           *crdt.Root
	presences      *presence.Map
	onlineClients  map[string]bool
	localChanges   []*change.Change
	detachedActors map[time.ActorID]int64  // NEW
}
```

Initialize in the constructor (find `NewInternalDocument` or equivalent):

```go
detachedActors: make(map[time.ActorID]int64),
```

- [ ] **Step 2: Add methods to InternalDocument**

```go
// DetachedActors returns the detached actors map.
func (d *InternalDocument) DetachedActors() map[time.ActorID]int64 {
	return d.detachedActors
}

// AddDetachedActors adds the given detached actors and removes them from VV.
func (d *InternalDocument) AddDetachedActors(actors map[time.ActorID]int64) {
	for actorID, lamport := range actors {
		d.detachedActors[actorID] = lamport
		d.changeID.VersionVector().Unset(actorID)
	}
}

// AugmentVV augments the given VV with detached actors' lamport values.
func (d *InternalDocument) AugmentVV(vv time.VersionVector) time.VersionVector {
	for actorID, lamport := range d.detachedActors {
		if _, ok := vv.Get(actorID); !ok {
			vv.Set(actorID, lamport)
		}
	}
	return vv
}
```

- [ ] **Step 3: Update applySnapshot to restore detachedActors**

In `pkg/document/internal_document.go`, update `applySnapshot`:

```go
func (d *InternalDocument) applySnapshot(snapshot []byte, vector time.VersionVector) error {
	rootObj, presences, detachedActors, err := converter.BytesToSnapshot(snapshot)
	if err != nil {
		return err
	}

	d.root = crdt.NewRoot(rootObj)
	d.presences = presences
	d.detachedActors = detachedActors
	d.changeID = d.changeID.SetClocks(vector.MaxLamport(), vector)

	return nil
}
```

- [ ] **Step 4: Update ApplyChangePack to process detached_actors**

In `pkg/document/document.go`, update `ApplyChangePack`:

```go
func (d *Document) ApplyChangePack(pack *change.Pack) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 01. Process detached actors from server signal.
	if len(pack.DetachedActors) > 0 {
		d.doc.AddDetachedActors(pack.DetachedActors)
	}

	// 02. Apply remote changes to both the cloneRoot and the document.
	hasSnapshot := len(pack.Snapshot) > 0
	// ... rest of existing logic
```

- [ ] **Step 5: Augment change VV before applying remote changes**

Find where remote changes are applied (the `applyChanges` method or equivalent).
Before each change is executed, augment its VV:

```go
// Before executing the change:
d.doc.AugmentVV(c.ID().VersionVector())
```

- [ ] **Step 6: Augment minVV before GC**

In `ApplyChangePack`, update the GC call:

```go
// 04. Do Garbage collection.
if !d.options.DisableGC && !hasSnapshot {
	augmentedVV := d.doc.AugmentVV(pack.VersionVector.DeepCopy())
	d.GarbageCollect(augmentedVV)
}
```

- [ ] **Step 7: Verify build and tests**

Run: `make build && make test`
Expected: BUILD SUCCESS, PASS

- [ ] **Step 8: Commit**

```bash
git add pkg/document/
git commit -m "Add detachedActors to Document for VV augmentation on change execution and GC"
```

---

### Task 7: Snapshot Creation — Include detachedActors

**Files:**
- Modify: Server-side snapshot creation (find where `SnapshotToBytes` is called)

- [ ] **Step 1: Update snapshot creation to pass detachedActors**

Find the snapshot creation call site (likely in `server/packs/` or
`server/backend/database/`). Update to pass `detachedActors`:

The server creates snapshots from the document state. The document's
`detachedActors` should be included. This requires the server to:
1. Track which actors are detached but not yet fully cleaned up
2. Pass that information when creating the snapshot

Find the exact call site of `SnapshotToBytes` and update accordingly.

- [ ] **Step 2: Verify build and tests**

Run: `make build && make test`
Expected: BUILD SUCCESS, PASS

- [ ] **Step 3: Commit**

```bash
git add server/
git commit -m "Include detachedActors in snapshot creation for state restoration"
```

---

### Task 8: MongoDB — Update Detach Handling in UpdateClientInfoAfterPushPull

**Files:**
- Modify: `server/backend/database/mongo/client.go`
- Modify: `server/backend/database/memory/database.go`

- [ ] **Step 1: Update MongoDB detach BSON update**

In `server/backend/database/mongo/client.go`, in the detach branch of
`UpdateClientInfoAfterPushPull` (around line 1259), add `detached_lamport`:

```go
} else {
	updater = bson.M{
		"$set": bson.M{
			clientDocInfoKey(docInfo.ID, "server_seq"):        0,
			clientDocInfoKey(docInfo.ID, "client_seq"):        0,
			clientDocInfoKey(docInfo.ID, StatusKey):            clientDocInfo.Status,
			clientDocInfoKey(docInfo.ID, "detached_lamport"):   clientDocInfo.DetachedLamport,
			"updated_at":                                       info.UpdatedAt,
		},
		"$pull": bson.M{
			"attached_docs":  docInfo.ID,
			"attaching_docs": docInfo.ID,
		},
	}
}
```

- [ ] **Step 2: Update Memory DB equivalently**

Ensure the memory DB implementation also persists DetachedLamport on detach.

- [ ] **Step 3: Run integration tests**

Run: `make test -tags integration` (if MongoDB is available)
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/backend/database/
git commit -m "Persist DetachedLamport in MongoDB and Memory DB on detach"
```

---

### Task 9: Integration Tests

**Files:**
- Create or modify: `test/integration/vv_cleanup_test.go` (or add to existing VV test file)

- [ ] **Step 1: Write test for basic detach → cleanup cycle**

```go
func TestVVCleanupAfterDetach(t *testing.T) {
	// 1. Client A and B attach to the same document
	// 2. Both clients make edits (so both appear in each other's VV)
	// 3. Client B detaches
	// 4. Client A syncs — should eventually receive detached_actors with B's actorID
	// 5. Verify Client A's VV no longer contains B's actorID
	// 6. Verify Client A can still apply changes correctly (causality preserved)
}
```

- [ ] **Step 2: Write test for GC after VV cleanup**

```go
func TestGCAfterVVCleanup(t *testing.T) {
	// 1. Client A and B attach, both edit
	// 2. Client A deletes an element
	// 3. Client B syncs (receives the delete)
	// 4. Client B detaches
	// 5. Client A syncs — receives detached_actors
	// 6. Client A triggers GC — verify the deleted element is collected
	//    (minVV augmented with detachedActors should allow GC)
}
```

- [ ] **Step 3: Write test for snapshot restore with detachedActors**

```go
func TestSnapshotRestoreWithDetachedActors(t *testing.T) {
	// 1. Client A and B attach, both edit, B detaches
	// 2. Client A syncs and receives detached_actors
	// 3. Force snapshot creation
	// 4. New Client C attaches — receives snapshot
	// 5. Verify Client C has B in detachedActors
	// 6. Verify Client C can process changes correctly
}
```

- [ ] **Step 4: Run all tests**

Run: `make test`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add test/
git commit -m "Add integration tests for VV cleanup after client detach"
```

---

### Task 10: Lint and Final Verification

- [ ] **Step 1: Run linter**

Run: `make lint`
Expected: No errors

- [ ] **Step 2: Run full test suite**

Run: `make test`
Expected: ALL PASS

- [ ] **Step 3: Fix any issues found**

- [ ] **Step 4: Final commit if needed**

```bash
git commit -m "Fix lint issues in VV cleanup implementation"
```

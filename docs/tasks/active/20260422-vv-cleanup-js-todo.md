# VV Cleanup — JS SDK Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Created**: 2026-04-22

**Goal:** Apply VV cleanup to JS SDK so clients can remove detached actors from VV and augment change VV / minVV for correct causality and GC.

**Architecture:** When the server sends `detached_actors` in ChangePack, the JS client stores them in a `detachedActors` map on Document, removes them from the document's VV, and augments change VV before execution and minVV before GC. Snapshots also carry `detached_actors` for state restoration.

**Prerequisite:** Go PR (server-side changes) must be merged first. Proto changes (`detached_actors` on ChangePack field 8 and Snapshot field 3) are already in place.

**Design doc:** `docs/design/vv-cleanup.md` (in yorkie repo)

---

### Task 1: Proto Types Update

After the Go PR merges and `make proto` regenerates the proto files, the JS SDK
proto types need to be regenerated from the updated `.proto` files.

**Files:**
- Modify: `packages/sdk/src/api/yorkie/v1/resources_pb.ts` (auto-generated)

- [ ] **Step 1: Regenerate proto types**

Run the SDK's proto generation command (check package.json for the exact script).
After regeneration, verify that `ChangePack` now has `detachedActors` field and
`Snapshot` has `detachedActors` field in `resources_pb.ts`.

- [ ] **Step 2: Verify the generated types**

Check that `resources_pb.ts` contains:

```typescript
// In ChangePack type:
detachedActors: { [key: string]: bigint };

// In Snapshot type:
detachedActors: { [key: string]: bigint };
```

- [ ] **Step 3: Commit**

```bash
git add packages/sdk/src/api/yorkie/v1/
git commit -m "Regenerate proto types with detached_actors fields"
```

---

### Task 2: ChangePack Class — Add detachedActors

**Files:**
- Modify: `packages/sdk/src/document/change/change_pack.ts`

- [ ] **Step 1: Add detachedActors field and update constructor**

```typescript
export class ChangePack<P extends Indexable> {
  private documentKey: string;
  private checkpoint: Checkpoint;
  private isRemoved: boolean;
  private changes: Array<Change<P>>;
  private snapshot?: Uint8Array;
  private versionVector?: VersionVector;
  private detachedActors: Map<string, bigint>;  // NEW

  constructor(
    key: string,
    checkpoint: Checkpoint,
    isRemoved: boolean,
    changes: Array<Change<P>>,
    versionVector?: VersionVector,
    snapshot?: Uint8Array,
    detachedActors?: Map<string, bigint>,
  ) {
    this.documentKey = key;
    this.checkpoint = checkpoint;
    this.isRemoved = isRemoved;
    this.changes = changes;
    this.snapshot = snapshot;
    this.versionVector = versionVector;
    this.detachedActors = detachedActors ?? new Map();
  }
```

- [ ] **Step 2: Update static create method**

```typescript
  public static create<P extends Indexable>(
    key: string,
    checkpoint: Checkpoint,
    isRemoved: boolean,
    changes: Array<Change<P>>,
    versionVector?: VersionVector,
    snapshot?: Uint8Array,
    detachedActors?: Map<string, bigint>,
  ): ChangePack<P> {
    return new ChangePack<P>(
      key,
      checkpoint,
      isRemoved,
      changes,
      versionVector,
      snapshot,
      detachedActors,
    );
  }
```

- [ ] **Step 3: Add getter**

```typescript
  /**
   * `getDetachedActors` returns the detached actors map from the server signal.
   */
  public getDetachedActors(): Map<string, bigint> {
    return this.detachedActors;
  }
```

- [ ] **Step 4: Verify build**

Run: `npm run build` (or the project's build command)
Expected: BUILD SUCCESS

- [ ] **Step 5: Commit**

```bash
git add packages/sdk/src/document/change/change_pack.ts
git commit -m "Add detachedActors field to ChangePack class"
```

---

### Task 3: Converter — Parse detached_actors

**Files:**
- Modify: `packages/sdk/src/api/converter.ts`

- [ ] **Step 1: Update fromChangePack to parse detached_actors**

At line 1459, update `fromChangePack`:

```typescript
function fromChangePack<P extends Indexable>(
  pbPack: PbChangePack,
): ChangePack<P> {
  const detachedActors = new Map<string, bigint>();
  for (const [key, value] of Object.entries(pbPack.detachedActors)) {
    const hexKey = bytesToHex(base64ToUint8Array(key));
    detachedActors.set(hexKey, BigInt(value.toString()));
  }

  return ChangePack.create<P>(
    pbPack.documentKey!,
    fromCheckpoint(pbPack.checkpoint!),
    pbPack.isRemoved,
    fromChanges(pbPack.changes),
    fromVersionVector(pbPack.versionVector),
    pbPack.snapshot,
    detachedActors,
  );
}
```

- [ ] **Step 2: Update toChangePack to serialize detached_actors**

At line 871, update `toChangePack`:

```typescript
function toChangePack(pack: ChangePack<Indexable>): PbChangePack {
  const pbDetachedActors: { [key: string]: bigint } = {};
  for (const [actorID, lamport] of pack.getDetachedActors()) {
    const base64ActorID = uint8ArrayToBase64(toUint8Array(actorID));
    pbDetachedActors[base64ActorID] = lamport;
  }

  return create(PbChangePackSchema, {
    documentKey: pack.getDocumentKey(),
    checkpoint: toCheckpoint(pack.getCheckpoint()),
    isRemoved: pack.getIsRemoved(),
    changes: toChanges(pack.getChanges()),
    snapshot: pack.getSnapshot(),
    versionVector: toVersionVector(pack.getVersionVector()),
    detachedActors: pbDetachedActors,
  });
}
```

- [ ] **Step 3: Update bytesToSnapshot to return detached_actors**

At line 1642, update `bytesToSnapshot`:

```typescript
function bytesToSnapshot<P extends Indexable>(
  bytes?: Uint8Array,
): {
  root: CRDTObject;
  presences: Map<ActorID, P>;
  detachedActors: Map<string, bigint>;
} {
  if (!bytes) {
    return {
      root: CRDTObject.create(InitialTimeTicket),
      presences: new Map(),
      detachedActors: new Map(),
    };
  }

  const snapshot = fromBinary(PbSnapshotSchema, bytes);
  const detachedActors = new Map<string, bigint>();
  for (const [key, value] of Object.entries(snapshot.detachedActors)) {
    const hexKey = bytesToHex(base64ToUint8Array(key));
    detachedActors.set(hexKey, BigInt(value.toString()));
  }

  return {
    root: fromElement(snapshot.root!) as CRDTObject,
    presences: fromPresences<P>(snapshot.presences),
    detachedActors,
  };
}
```

- [ ] **Step 4: Fix all callers of bytesToSnapshot**

Search for `bytesToSnapshot` call sites and update to destructure `detachedActors`.
Key location: `document.ts:applySnapshot`.

- [ ] **Step 5: Verify build**

Run: `npm run build`
Expected: BUILD SUCCESS

- [ ] **Step 6: Commit**

```bash
git add packages/sdk/src/api/converter.ts
git commit -m "Add detached_actors serialization to converter"
```

---

### Task 4: Document — detachedActors Map and VV Augmentation

**Files:**
- Modify: `packages/sdk/src/document/document.ts`

- [ ] **Step 1: Add detachedActors field to Document class**

In the class field declarations (around line 532):

```typescript
  private detachedActors: Map<string, bigint>;
```

Initialize in the constructor:

```typescript
  this.detachedActors = new Map();
```

- [ ] **Step 2: Add augmentVV helper method**

```typescript
  /**
   * `augmentVV` augments the given version vector with detached actors'
   * lamport values for correct causality detection and GC.
   */
  private augmentVV(vv: VersionVector): VersionVector {
    for (const [actorID, lamport] of this.detachedActors) {
      if (!vv.has(actorID)) {
        vv.set(actorID, lamport);
      }
    }
    return vv;
  }
```

- [ ] **Step 3: Add addDetachedActors helper method**

```typescript
  /**
   * `addDetachedActors` stores detached actors and removes them from the
   * document's version vector.
   */
  private addDetachedActors(actors: Map<string, bigint>): void {
    for (const [actorID, lamport] of actors) {
      this.detachedActors.set(actorID, lamport);
      this.changeID.getVersionVector().unset(actorID);
    }
  }
```

- [ ] **Step 4: Update applyChangePack to process detached_actors**

At line 1097, update `applyChangePack`:

```typescript
  public applyChangePack(pack: ChangePack<P>): void {
    // 01. Process detached actors from server signal.
    const detachedActors = pack.getDetachedActors();
    if (detachedActors.size > 0) {
      this.addDetachedActors(detachedActors);
    }

    // 02. Apply snapshot or changes to the root object.
    if (pack.hasSnapshot()) {
      this.applySnapshot(
        pack.getCheckpoint().getServerSeq(),
        pack.getVersionVector()!,
        pack.getSnapshot()!,
        pack.getCheckpoint().getClientSeq(),
      );
    } else {
      this.applyChanges(pack.getChanges(), OpSource.Remote);
      this.removePushedLocalChanges(pack.getCheckpoint().getClientSeq());
    }

    // 03. Update the checkpoint.
    this.checkpoint = this.checkpoint.forward(pack.getCheckpoint());

    // 04. Do Garbage collection.
    if (!pack.hasSnapshot()) {
      const augmentedVV = this.augmentVV(pack.getVersionVector()!.deepcopy());
      this.garbageCollect(augmentedVV);
    }

    // 05. Update the status.
    if (pack.getIsRemoved()) {
      this.applyStatus(DocStatus.Removed);
    }

    if (logger.isEnabled(LogLevel.Trivial)) {
      logger.trivial(`${this.root.toJSON()}`);
    }
  }
```

- [ ] **Step 5: Augment change VV before execution**

In the `applyChange` method (around line 1441), augment the change's VV
before executing:

```typescript
  private applyChange(change: Change<P>, source: OpSource): void {
    // Augment change VV with detached actors for causality detection.
    if (source === OpSource.Remote) {
      const changeVV = change.getID().getVersionVector();
      if (changeVV) {
        this.augmentVV(changeVV);
      }
    }

    // ... rest of existing applyChange logic
  }
```

- [ ] **Step 6: Update applySnapshot to restore detachedActors**

Update `applySnapshot` (line 1356):

```typescript
  public applySnapshot(
    serverSeq: bigint,
    snapshotVector: VersionVector,
    snapshot?: Uint8Array,
    clientSeq: number = -1,
  ) {
    const { root, presences, detachedActors } =
      converter.bytesToSnapshot<P>(snapshot);
    this.root = new CRDTRoot(root);
    this.presences = presences;
    this.detachedActors = detachedActors;
    this.changeID = this.changeID.setClocks(
      snapshotVector.maxLamport(),
      snapshotVector,
    );

    // ... rest of existing logic unchanged
  }
```

- [ ] **Step 7: Verify build**

Run: `npm run build`
Expected: BUILD SUCCESS

- [ ] **Step 8: Commit**

```bash
git add packages/sdk/src/document/document.ts
git commit -m "Add detachedActors to Document for VV augmentation"
```

---

### Task 5: Unit Tests

**Files:**
- Create: `packages/sdk/test/unit/document/vv_cleanup_test.ts` (or add to existing test file)

- [ ] **Step 1: Write test for addDetachedActors**

```typescript
import { Document } from '@yorkie-js-sdk/src/document/document';
import { vectorOf } from '../helper/helper';

describe('VV Cleanup', () => {
  it('should remove detached actors from VV', () => {
    // Setup: create document with VV containing actors A, B
    // Act: call addDetachedActors with actor B
    // Assert: document VV no longer has actor B
    // Assert: detachedActors map contains actor B
  });
});
```

- [ ] **Step 2: Write test for augmentVV**

```typescript
  it('should augment VV with detached actors', () => {
    // Setup: document has detachedActors = { B: 5 }
    // Act: augmentVV on a VV that lacks actor B
    // Assert: augmented VV contains B with lamport 5
  });

  it('should not overwrite existing VV entry when augmenting', () => {
    // Setup: document has detachedActors = { B: 5 }
    // Act: augmentVV on a VV that already has B with lamport 10
    // Assert: VV still has B with lamport 10 (not overwritten)
  });
```

- [ ] **Step 3: Write test for ChangePack detached_actors round-trip**

```typescript
  it('should serialize and deserialize detached_actors in ChangePack', () => {
    // Setup: create ChangePack with detachedActors
    // Act: toChangePack → fromChangePack
    // Assert: detachedActors preserved
  });
```

- [ ] **Step 4: Run tests**

Run: `npm test` (or the project's test command)
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add packages/sdk/test/
git commit -m "Add unit tests for VV cleanup"
```

---

### Task 6: Integration Tests

**Files:**
- Modify: `packages/sdk/test/integration/gc_test.ts` or create new test file

- [ ] **Step 1: Write integration test for detach → VV cleanup cycle**

Using the existing `withTwoClientsAndDocuments` helper:

```typescript
it('should clean up VV after client detach', async () => {
  // 1. Client A and B attach to document
  // 2. Both make edits (both actors in each other's VV)
  // 3. Client B detaches
  // 4. Client A syncs multiple times until detached_actors received
  // 5. Verify Client A's VV no longer contains B's actorID
  // 6. Client A can still make edits and sync correctly
});
```

- [ ] **Step 2: Write integration test for GC with detached actors**

```typescript
it('should garbage collect correctly after VV cleanup', async () => {
  // 1. Client A and B attach, both edit
  // 2. Client A deletes an element
  // 3. Client B syncs (receives delete), then detaches
  // 4. Client A syncs (receives detached_actors)
  // 5. Verify GC count is correct (augmented minVV allows GC)
});
```

- [ ] **Step 3: Run integration tests**

Run: `npm run test:integration` (or equivalent)
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add packages/sdk/test/
git commit -m "Add integration tests for VV cleanup with client detach"
```

---

### Task 7: Lint and Final Verification

- [ ] **Step 1: Run linter**

Run: `npm run lint`
Expected: No errors

- [ ] **Step 2: Run full test suite**

Run: `npm test`
Expected: ALL PASS

- [ ] **Step 3: Fix any issues**

- [ ] **Step 4: Final commit if needed**

```bash
git commit -m "Fix lint issues in VV cleanup implementation"
```

**Created**: 2026-06-16

# Presenceless Document Option

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Document-scope option `disable_presence` so a document
can declare it never accepts or stores presence. Driven by high
fan-out Counter-only documents where presence accumulates monotonically
on the server because clients reach end-of-life without sending
`detach` — observed in real workloads with tens of thousands of stale
actors and attach responses inflated to hundreds of kilobytes. Once a
document is created with the flag, the server strips presence on every
PushPull regardless of which client attached, so other-client
`presence.put/clear` cannot pollute the document map.

**Architecture (as designed):** The flag is declared at first attach
through `AttachDocumentRequest.disable_presence`, persisted on
`DocInfo.DisablePresence` via mongo `$setOnInsert` (immutable
thereafter), and read by the PushPull handler into
`PushPullOptions.DisablePresence`. The server (a) strips presence
changes from every PushPull entry, (b) serializes an empty presence
map in pullSnapshot/storeSnapshot, and (c) defensively drops any
stray cached presence on the read path. The Go SDK exposes
`WithDisablePresence()`, sends the wire field on first attach,
reads the server-fixated value back from the response, and gates
its own `Document.Update` so opt-in clients silently no-op on
presence updates. JS SDK ships as a follow-up PR (separate task
doc).

**Tech Stack:** Go server (`server/`), Go SDK (`client/`,
`pkg/document/`), MongoDB + memory backends, `-tags integration`
end-to-end test.

**Branch:** `feat/presenceless-document-option`

**Related work:**
- `docs/design/disable-gc-on-attach.md` — analogous opt-out pattern.
  Differs in: (a) doc-scope vs per-attach, (b) server-persisted on
  `DocInfo` vs wire-only.
- `docs/design/doc-presence.md` — background on the presence model
  this option opts out of.
- `docs/design/housekeeping.md` — orthogonal track for cleaning up
  stale `ClientInfo` rows.

---

## Background

### Symptom

A daily-rotated, Counter-only document (`SyncMode.Manual`,
`disableGC=true`) increments a `pv` counter once on mount and never
uses presence functionally, yet the JS SDK still emits an empty PUT
presence change at attach. Combined with users leaving the page
without sending `detach` (mobile background, iOS Safari, network
drop), the document's presence map grows monotonically.

Representative measurement (two snapshots ~2.5 hours apart):

| server_seq | root.bytes | presences.entries | presences.bytes |
|---|---|---|---|
| 221016 | 155 | 28,167 | 788,676 |
| 223516 | 155 | 28,485 | 797,580 |

`root` is fixed at 155 bytes; only the presence map grows. AttachDocument
response is ~170KB at this point, on a page where every other RPC
returns <0.5KB.

### Why a per-attach opt-out is insufficient

Other client (older SDK / a teammate's code) that does not pass the
flag would re-pollute the document's presence map because presence
is a document-shared map, not a per-client value. A doc-scoped
declaration with server-side enforcement is what closes the source.

### Why immutable (first-attach fixate)

The flag travels on `AttachDocumentRequest`, is persisted via
`$setOnInsert`, and never changes afterward. This avoids a toggle
race (cache invalidation across the cluster) and matches the natural
rollout — operators introduce the option by creating the next-day
document key with it set. Existing documents stay on the default
(`false`) path with no migration.

### Compared to candidate alternatives

A separate evaluation compared four candidates: (1) per-client
opt-out, (2) project-scoped setting, (3) JS SDK pagehide detach
hardening, (4) this proposal. Candidate 2 was QA-verified on a
sibling branch. Candidate 4 is chosen as the structural option
closest to how the user already declares document properties
(`disableGC`) while keeping the
enforcement document-scoped so a single misconfigured client cannot
break the guarantee.

---

## File Map

### Go server (this PR)

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `api/yorkie/v1/yorkie.proto` | Add `disable_presence` to `AttachDocumentRequest` (field 5) and `AttachDocumentResponse` (field 5) |
| Modify | `api/yorkie/v1/yorkie.pb.go` | Regenerated via `make proto` |
| Modify | `server/backend/database/doc_info.go` | Add `DisablePresence bool` field; update `DeepCopy()` |
| Modify | `server/backend/database/mongo/client.go` | `FindOrCreateDocInfo` accepts `disablePresence bool`, uses `$setOnInsert` |
| Modify | `server/backend/database/memory/database.go` | Mirror the new signature; same setOnInsert semantics in memory |
| Modify | `server/backend/database/testcases/testcases.go` | Round-trip `DisablePresence` if existing tests touch `FindOrCreateDocInfo` |
| Modify | `server/documents/documents.go` | `FindOrCreateDocInfo` wrapper passes `disablePresence` through |
| Modify | `server/rpc/yorkie_server.go` | `AttachDocument` forwards `req.Msg.DisablePresence`, warns on wire/persisted mismatch; `PushPullChanges` fetches `DocInfo` and forwards `DisablePresence` into `PushPullOptions` |
| Modify | `server/packs/pushpull.go` | `PushPullOptions.DisablePresence`; strip presence at PushPull entry; thread `opts` through `preparePack`/`pullSnapshot`; defensive guard in `pullChangeInfos` |
| Add | `server/packs/strip.go` | `stripPresenceChanges` helper |
| Modify | `server/packs/snapshot.go` | `storeSnapshot` takes `disablePresence bool`; calls `doc.ResetPresences()` before `CreateSnapshotInfo` |
| Modify | `pkg/document/internal_document.go` | `ResetPresences()` helper |
| Modify | `pkg/document/change/change.go` | `SetPresenceChange`, `HasOperations` on `Change` |
| Modify | `pkg/document/change/context.go` | `HasPresenceChange`, `DropPresenceChange` on `Context` |

### Go SDK (this PR)

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `client/options.go` | `AttachOptions.DisablePresence`; `WithDisablePresence()` |
| Modify | `client/client.go` | Send `disable_presence` on attach; store the response-fixated value; skip initial `Presence.Initialize` when opt-out |
| Modify | `client/attachment.go` | Carry `disablePresence` on the per-doc attachment record |
| Modify | `pkg/document/document.go` | `Options.DisablePresence` + `WithDisablePresence`; `SetDisablePresence`; gate the presence-change emit inside `Update` |

### Tests

| Action | File | Responsibility |
|--------|------|----------------|
| Add | `pkg/document/internal_document_test.go` | `ResetPresences` clears the presence map |
| Add | `pkg/document/change/change_test.go` | `SetPresenceChange(nil)` preserves operations; `HasOperations` distinguishes presence-only |
| Add | `server/packs/strip_test.go` | `stripPresenceChanges` shape coverage (presence-only / mixed / ops-only / empty) |
| Add | `test/integration/presenceless_document_test.go` | End-to-end: attach with opt-out, presence put silently no-ops, response/cache/snapshot all empty |
| Add (build tag `bench`) | `test/bench/strip_presence_changes_bench_test.go` | Allocation-free strip across shapes |

---

## Task 1: Proto — `disable_presence` on Attach request/response

**Files:**
- Modify: `api/yorkie/v1/yorkie.proto`

- [x] **Step 1: Add the request field**

```protobuf
message AttachDocumentRequest {
  string client_id = 1;
  ChangePack change_pack = 2;
  string schema_key = 3;
  bool disable_gc = 4;
  // disable_presence declares that this document does not produce,
  // consume, or store presence. Honored on first attach only; later
  // attaches observe the value persisted in DocInfo.
  bool disable_presence = 5;
}
```

- [x] **Step 2: Add the response field**

```protobuf
message AttachDocumentResponse {
  string document_id = 1;
  ChangePack change_pack = 2;
  int32 max_size_per_document = 3;
  repeated Rule schema_rules = 4;
  bool disable_presence = 5;  // server-fixated value
}
```

- [x] **Step 3: Regenerate**

Run: `make proto`
Expected: only `api/yorkie/v1/yorkie.pb.go` and its `.connect.go`
companion change. No other generated files should move.

- [x] **Step 4: Commit**

```sh
git add api/yorkie/v1/yorkie.proto api/yorkie/v1/yorkie.pb.go api/yorkie/v1/v1connect/yorkie.connect.go
git commit -m "Add disable_presence fields to AttachDocument RPC"
```

---

## Task 2: `DocInfo.DisablePresence` schema

**Files:**
- Modify: `server/backend/database/doc_info.go`

- [x] **Step 1: Add the field**

```go
type DocInfo struct {
    // ... existing fields ...
    Epoch       int64     `bson:"epoch"`

    // DisablePresence declares that this document does not accept
    // presence put/clear. Written via $setOnInsert at first attach
    // and immutable thereafter; the empty (false) value matches
    // documents created before this option existed.
    DisablePresence bool `bson:"disable_presence,omitempty"`
}
```

- [x] **Step 2: Extend `DeepCopy()`**

Add `DisablePresence: d.DisablePresence` so the clone path used by
caches and tests carries the flag.

- [x] **Step 3: Verify**

Run: `go test ./server/backend/database/...`
Expected: PASS.

- [x] **Step 4: Commit**

```sh
git add server/backend/database/doc_info.go
git commit -m "Add DisablePresence to DocInfo schema"
```

---

## Task 3: `FindOrCreateDocInfo` — `$setOnInsert` fixate (mongo)

**Files:**
- Modify: `server/backend/database/mongo/client.go`

- [x] **Step 1: Extend the signature**

```go
func (c *Client) FindOrCreateDocInfo(
    ctx context.Context,
    clientRefKey types.ClientRefKey,
    docKey key.Key,
    disablePresence bool,
) (*database.DocInfo, error) {
```

- [x] **Step 2: Include the field in `$setOnInsert`**

```go
"$setOnInsert": bson.M{
    "owner":            clientRefKey.ClientID,
    "server_seq":       0,
    "created_at":       now,
    "updated_at":       now,
    "disable_presence": disablePresence,
},
```

The `$setOnInsert` operator only writes on actual inserts. Second
and later attaches observe the persisted value and ignore the wire
field — that is the immutability guarantee.

- [x] **Step 3: Duplicate-key fallback already returns the persisted row**

Confirm the existing `FindOne` fallback after `IsDuplicateKeyError`
returns the document as written, so the late attacher sees the
first writer's value.

- [x] **Step 4: Verify**

Run: `make test`
Expected: existing backend tests PASS.

- [x] **Step 5: Commit**

```sh
git add server/backend/database/mongo/client.go
git commit -m "Fixate DisablePresence on FindOrCreateDocInfo upsert"
```

---

## Task 4: Memory backend parity

**Files:**
- Modify: `server/backend/database/memory/database.go`

- [x] **Step 1: Match the new signature**

The in-memory `FindOrCreateDocInfo` accepts `disablePresence bool`
and applies it only when creating a new entry. An existing entry
keeps its current value.

- [x] **Step 2: Mirror tests if existing table touches the signature**

Run: `go test ./server/backend/database/memory/...`
Expected: PASS.

- [x] **Step 3: Commit**

```sh
git add server/backend/database/memory/database.go server/backend/database/testcases/
git commit -m "Memory backend honors DisablePresence at insert"
```

---

## Task 5: RPC handlers — forward flag and warn on mismatch

**Files:**
- Modify: `server/documents/documents.go`
- Modify: `server/rpc/yorkie_server.go`

- [x] **Step 1: Plumb through `documents.FindOrCreateDocInfo`**

The thin wrapper in `server/documents/documents.go` accepts and
forwards `disablePresence`.

- [x] **Step 2: `AttachDocument` handler**

```go
// server/rpc/yorkie_server.go
docInfo, err := documents.FindOrCreateDocInfo(
    ctx, s.backend, clientInfo, pack.DocumentKey, req.Msg.DisablePresence,
)
// ...
if req.Msg.DisablePresence != docInfo.DisablePresence {
    logging.From(ctx).Warnf(
        "attach with disable_presence=%v but doc fixated to %v: %s",
        req.Msg.DisablePresence, docInfo.DisablePresence, docInfo.Key,
    )
}
// ...
return connect.NewResponse(&api.AttachDocumentResponse{
    DocumentId:      docInfo.ID.String(),
    ChangePack:      pbChangePack,
    // ... existing fields ...
    DisablePresence: docInfo.DisablePresence,   // server-fixated
}), nil
```

`PushPull` is called with `PushPullOptions{DisablePresence: docInfo.DisablePresence}`.

- [x] **Step 3: `PushPullChanges` handler — fetch DocInfo first**

The handler today does not fetch `DocInfo`. Add one call (it is
cached via `docCache` so the cost is amortized) before the
`packs.PushPull` invocation and forward `docInfo.DisablePresence`
through `PushPullOptions`.

- [x] **Step 4: Verify**

Run: `go test ./server/rpc/...`
Expected: PASS.

- [x] **Step 5: Commit**

```sh
git add server/documents/documents.go server/rpc/yorkie_server.go
git commit -m "Forward DisablePresence through AttachDocument and PushPullChanges"
```

---

## Task 6: PushPull entry — `stripPresenceChanges` helper

**Files:**
- Add: `server/packs/strip.go`
- Modify: `server/packs/pushpull.go`
- Modify: `pkg/document/change/change.go`

- [x] **Step 1: Setter and operation accessor on `Change`**

In `pkg/document/change/change.go`, add:

```go
func (c *Change) SetPresenceChange(p *innerpresence.Change) {
    c.presenceChange = p
}

func (c *Change) HasOperations() bool {
    return len(c.operations) > 0
}
```

- [x] **Step 2: `stripPresenceChanges` helper**

```go
// server/packs/strip.go
//
// stripPresenceChanges removes PresenceChange from each Change for
// documents configured presenceless. Presence-only changes drop in
// full; mixed changes keep their operations and lose presence.
func stripPresenceChanges(changes []*change.Change) []*change.Change {
    if len(changes) == 0 {
        return changes
    }
    out := changes[:0]
    for _, c := range changes {
        if c.PresenceChange() != nil {
            if !c.HasOperations() {
                continue
            }
            c.SetPresenceChange(nil)
        }
        out = append(out, c)
    }
    return out
}
```

- [x] **Step 3: Strip at PushPull entry**

```go
// server/packs/pushpull.go — at the very top of PushPull
if opts.DisablePresence {
    reqPack.Changes = stripPresenceChanges(reqPack.Changes)
}
```

Also extend `PushPullOptions`:

```go
type PushPullOptions struct {
    Mode             types.SyncMode
    Status           document.StatusType
    DisableGC        bool
    DisablePresence  bool   // forwarded from docInfo
}
```

- [x] **Step 4: Verify**

Run: `go test ./pkg/document/change/... ./server/packs/...`
Expected: PASS.

- [x] **Step 5: Commit**

```sh
git add server/packs/strip.go server/packs/pushpull.go pkg/document/change/change.go
git commit -m "Strip presence changes at PushPull entry when document is presenceless"
```

---

## Task 7: `pullSnapshot` — empty presence in response snapshot

**Files:**
- Modify: `server/packs/pushpull.go`
- Modify: `pkg/document/internal_document.go`

- [x] **Step 1: `InternalDocument.ResetPresences()`**

```go
// pkg/document/internal_document.go
//
// ResetPresences clears the presence map and the online-clients set.
// Used by the server when serializing a snapshot for a presenceless
// document to prevent any earlier-cached presence entry from leaking
// onto the wire.
func (d *InternalDocument) ResetPresences() {
    d.presences = innerpresence.NewMap()
    d.onlineClients = make(map[string]bool)
}
```

- [x] **Step 2: Threading `opts` into `pullSnapshot`**

`preparePack` and `pullSnapshot` currently take no `PushPullOptions`.
Extend both signatures to receive it. In `pullSnapshot`:

```go
presences := doc.AllPresences()
if opts.DisablePresence {
    doc.ResetPresences()
    presences = nil
}
snapshot, err := converter.SnapshotToBytes(doc.RootObject(), presences)
```

Both belt-and-suspenders: the in-memory reset prevents downstream
leak (e.g. if the snapshot path branches), and the `nil` argument
ensures the wire bytes carry an empty map.

- [x] **Step 3: Verify**

Run: `go test ./pkg/document/... ./server/packs/...`
Expected: PASS.

- [x] **Step 4: Commit**

```sh
git add pkg/document/internal_document.go server/packs/pushpull.go
git commit -m "Serialize empty presence map for presenceless documents"
```

---

## Task 8: `storeSnapshot` — empty presence in persisted snapshot

**Files:**
- Modify: `server/packs/snapshot.go`

- [x] **Step 1: Accept `disablePresence`**

```go
func storeSnapshot(
    ctx context.Context,
    be *backend.Backend,
    snapshotInterval int64,
    docInfo *database.DocInfo,
) error {
```

`docInfo` already carries `DisablePresence`. Right after the
`ApplyChangePack` step that rebuilds the in-memory document, reset:

```go
if docInfo.DisablePresence {
    doc.ResetPresences()
}
if err := be.DB.CreateSnapshotInfo(ctx, docRefKey, doc); err != nil {
    return err
}
```

This is defensive: if any earlier `snapshots` row was written before
the flag (cannot happen via this PR, but is the safe shape), the
in-memory doc would otherwise inherit it via `applySnapshot`.

- [x] **Step 2: Verify**

Run: `go test ./server/packs/...`
Expected: PASS.

- [x] **Step 3: Commit**

```sh
git add server/packs/snapshot.go
git commit -m "Persist empty presence map for presenceless documents"
```

---

## Task 9: `pullChangeInfos` — defensive read-path guard

**Files:**
- Modify: `server/packs/pushpull.go`

- [x] **Step 1: Receive `docInfo` and strip on read**

```go
for _, pulledChange := range pulled {
    if clientInfo.ID == pulledChange.ActorID &&
        cpAfterPush.ClientSeq >= pulledChange.ClientSeq {
        continue
    }
    if docInfo.DisablePresence {
        // Defensive: any stray presence in cache or older rows is
        // dropped on the way to the wire.
        if pulledChange.PresenceChange != nil {
            pulledChange = pulledChange.DeepCopy()
            pulledChange.PresenceChange = nil
        }
        if !pulledChange.HasOperations() {
            continue
        }
    }
    filteredChanges = append(filteredChanges, pulledChange)
}
```

`DeepCopy` keeps the cache's shared pointer untouched.

- [x] **Step 2: Verify**

Run: `go test ./server/packs/...`
Expected: PASS.

- [x] **Step 3: Commit**

```sh
git add server/packs/pushpull.go
git commit -m "Defensive presence strip on PushPull read path"
```

---

## Task 10: Go SDK — `WithDisablePresence` + response handling

**Files:**
- Modify: `client/options.go`
- Modify: `client/client.go`
- Modify: `client/attachment.go`
- Modify: `pkg/document/document.go`
- Modify: `pkg/document/change/context.go`

- [x] **Step 1: Attach option**

```go
// client/options.go
type AttachOptions struct {
    // ... existing fields ...
    DisableGC        bool
    DisablePresence  bool
}

func WithDisablePresence() AttachOption {
    return func(o *AttachOptions) { o.DisablePresence = true }
}
```

- [x] **Step 2: Send on the wire, store response value on the attachment**

```go
// client/client.go (inside attachDocument)
res, err := c.client.AttachDocument(ctx, withShardKey(
    connect.NewRequest(&api.AttachDocumentRequest{
        ClientId:        c.id.String(),
        ChangePack:      pbChangePack,
        SchemaKey:       opts.Schema,
        DisableGc:       opts.DisableGC,
        DisablePresence: opts.DisablePresence,
    }), ...,
))
// ...
d.SetDisablePresence(res.Msg.DisablePresence)
c.attachments.Set(d.Key(), &Attachment{
    // ...
    disableGC:        opts.DisableGC,
    disablePresence:  res.Msg.DisablePresence,
})
```

Skip the initial `Presence.Initialize` call when the local option
declared opt-out — that way, an opt-in client never produces a
wire PUT for presenceless documents:

```go
if !opts.DisablePresence {
    if err := d.Update(func(_ *json.Object, p *document.Presence) error {
        p.Initialize(opts.Presence)
        return nil
    }); err != nil { return err }
}
```

- [x] **Step 3: Gate `Document.Update`**

```go
// pkg/document/document.go (inside Update, after the user callback)
if d.options.DisablePresence && ctx.HasPresenceChange() {
    ctx.DropPresenceChange()
    if !ctx.HasOperations() {
        return nil
    }
}
```

Add `HasPresenceChange()` / `DropPresenceChange()` on
`change.Context`. Add `SetDisablePresence` on `Document` to flip
the option after the attach response is processed.

A single `Logger.Warn` per document the first time the gate trips
(state held on the document) makes the silent-drop discoverable
without log spam.

- [x] **Step 4: Verify**

Run: `go test ./client/... ./pkg/document/...`
Expected: PASS.

- [x] **Step 5: Commit**

```sh
git add client/ pkg/document/
git commit -m "Add WithDisablePresence to Go SDK with response-driven gating"
```

---

## Task 11: Integration test

**Files:**
- Add: `test/integration/presenceless_document_test.go`

- [x] **Step 1: Write the test**

Build tag `integration`. Coverage:

1. **Fixate at first attach** — first client attaches with
   `WithDisablePresence()`; the mongo `documents` row carries
   `disable_presence: true`.
2. **Late attacher observes persisted value** — second client
   attaches without the option; response `DisablePresence` is
   still `true`; a warn log line was emitted.
3. **PUT presence is stripped** — opt-in (without option) client
   calls `doc.Update(p.Set(...))` after attach; the next
   PushPull response has an empty presence map; mongo `changes`
   row has `presence_change == nil`.
4. **storeSnapshot is clean** — drive enough updates to cross
   `snapshot_interval`; the resulting `snapshots` row decompresses
   to a document whose presence map is empty.
5. **Backward compatibility** — existing document (no field) attach
   has `DisablePresence == false` and the normal accumulation path
   continues to work.

Mirror the existing client setup in
`test/integration/disable_gc_test.go`.

- [x] **Step 2: Run**

```sh
docker compose -f build/docker/docker-compose.yml up --build -d
go test -tags integration ./test/integration/ -run TestPresenceless -v
```

Expected: all subtests PASS.

- [x] **Step 3: Full integration sweep**

Run: `make test`
Expected: no regressions (gc, snapshot, presence, vv-cleanup).

- [x] **Step 4: Commit**

```sh
git add test/integration/presenceless_document_test.go
git commit -m "Add integration tests for presenceless document option"
```

---

## Task 12: Benchmark — `stripPresenceChanges`

**Files:**
- Add: `test/bench/strip_presence_changes_bench_test.go`

- [x] **Step 1: Cover the four shapes**

Build tag `bench`. Inputs of size n ∈ {1, 8, 64} for shapes
`presence_only`, `mixed`, `ops_only`, and a size-0 input.

- [x] **Step 2: Run**

```sh
go test -tags bench ./test/bench/ -run x -bench BenchmarkStripPresenceChanges -benchmem
```

Expected: zero allocations across all shapes; sub-microsecond at
n=64 for the worst shape.

- [x] **Step 3: Commit**

```sh
git add test/bench/strip_presence_changes_bench_test.go
git commit -m "Benchmark stripPresenceChanges across shapes"
```

---

## Task 13: Self-review and PR

- [x] **Step 1: Lint and unit tests**

```sh
make lint
go test ./...
```

Expected: clean.

- [ ] **Step 2: Self-review**

Dispatch `superpowers:requesting-code-review` (or `/code-review`)
over the full branch diff. Apply Critical/Important findings;
capture non-blocking notes in the lessons file.

- [x] **Step 3: Rebase on main and open PR** — yorkie-team/yorkie#1841

```sh
git fetch && git rebase origin/main
git push -u origin feat/presenceless-document-option
gh pr create --title "Add disable_presence Document option" \
  --body-file <(cat <<'EOF'
## Summary
- New `disable_presence` field on `AttachDocumentRequest` /
  `AttachDocumentResponse` and on `DocInfo`. Fixated via mongo
  `$setOnInsert` at first attach; immutable afterward.
- PushPull entry, snapshot serialization, and snapshot persistence
  all enforce an empty presence map for presenceless documents.
- Read-path defensive guard ensures no cached presence leaks onto
  the wire.
- Go SDK exposes `WithDisablePresence()`; gates `Document.Update`
  presence emit on the server-fixated value carried back in the
  attach response.

## Background
A high fan-out, Counter-only document accumulates stale presence
entries on the order of tens of thousands when clients reach
end-of-life without `detach`, inflating attach responses to hundreds
of kilobytes. See
`docs/tasks/active/20260616-presenceless-document-option-todo.md`
for the full measurement trail and alternatives considered.

## Test plan
- [ ] `make lint`
- [ ] `go test ./...`
- [ ] `go test -tags integration ./test/integration/ -run TestPresenceless`
- [ ] `make test` (full integration)
- [ ] `go test -tags bench ./test/bench/ -bench BenchmarkStripPresenceChanges -benchmem`

## Follow-up
- yorkie-js-sdk PR adds the matching client option (separate task
  doc).
EOF
)
```

- [ ] **Step 4: Address review and merge**

Resolve CodeRabbit + maintainer comments; rebase if `main` moved.

---

## Task 14: Lessons and archive

- [ ] **Step 1: Capture lessons**

Write findings to
`docs/tasks/active/20260616-presenceless-document-option-lessons.md`.
Likely topics:
- `$setOnInsert` race semantics in mongo and how the duplicate-key
  fallback path interacts with it
- Anything surprising in how the snapshot-build cache (`be.Cache`)
  interacts with `ResetPresences`
- Behaviour for old clients that do not know the field (server
  silent-enforces; SDK never warns because the field is not read)

- [ ] **Step 2: Archive**

```sh
bash scripts/tasks-archive.sh
bash scripts/tasks-index.sh
```

---

## Remaining

- [ ] All Task 1–14 steps above
- [ ] yorkie-js-sdk follow-up PR (separate task doc)
- [ ] Future: admin RPC to toggle the flag after creation (deferred;
      out of scope here)

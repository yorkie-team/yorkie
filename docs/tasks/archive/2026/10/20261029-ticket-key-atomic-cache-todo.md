# Make the lazy ticket and node-ID key caches race-free

**Created**: 2026-10-29

Issue #2305, which absorbs #2302, #2303 and #2304. Follow-up to #2248 / #2301.

## Problem

`Ticket.Key()`, `Ticket.ActorIDBase64()` (`pkg/document/time/ticket.go`) and
`RGATreeSplitNodeID.key()` (`pkg/document/crdt/rga_tree_split.go`) fill a
string cache on first call with a plain write. #2301 pre-warmed only the
package singletons (`InitialTicket`, `MaxTicket`, `initialNodeID`) in
`init()`, on the premise that every other ticket lives behind one document's
lock. That premise does not hold on the server:

- `BuildInternalDocForServerSeq` (`server/packs/snapshot.go`) hands each
  request a `DeepCopy()` of the cached `InternalDocument` and applies the
  changes stored after it. `Object.DeepCopy` and `RGATreeSplitNode.DeepCopy`
  reuse the `*time.Ticket` and `*RGATreeSplitNodeID` pointers. A request that
  deletes a live text or tree node keys an ID that was never keyed, and that ID
  is shared with the cache and with every other request's copy.

Reproduced on main with `go test -race`:

- `(*RGATreeSplitNodeID).key()` written from `RGATreeSplit.deleteNodes`
- `(*Ticket).Key()` written from `TreeNodeID.toIDString` → `Root.RegisterGCPair`

Read-only paths (concurrent `DeepCopy`, `GarbageLen` under `RLock`) do not race
on main. `Root.RegisterElement` keys every element's `createdAt` while the
document is built, so those keys are already warm.

## Options

| Option | Verdict |
|---|---|
| Warm only the shared singletons (#2301) | Leaves the snapshot-cache path above racing |
| Compute keys eagerly in the constructors (#2301 first draft) | Race-free, but +21–149% allocs/op on Document/text, RemoteApply, ChangeReplay, Array/random_delete: most IDs are never keyed |
| `sync.Once` per cache | 12 bytes plus a mutex per ticket; heavier than needed |
| `atomic.Pointer[string]`, lazy | Race-free and lazy, but holds a `*string`: `reflect.DeepEqual` compares the box by address, so two logically identical tickets stop comparing equal once both are warm |
| **`atomic.Value` holding a string, lazy** | Chosen. Same laziness, and `DeepEqual` compares an interface by its dynamic value, so equality matches main's plain `string` field. A concurrent miss computes the same string twice and the last store wins, which is harmless |

## Tasks

- [x] Race test reproducing the snapshot-cache pattern from both a
      converter-decoded change set and a snapshot
      (`pkg/document/shared_ticket_race_test.go`). The apply-on-clone
      subtests are Red on main. The read-only subtests pass on main and
      are kept as regression guards
- [x] Unit race tests on fresh, non-singleton `Ticket` and
      `RGATreeSplitNodeID`; Red on main
- [x] `Ticket.cachedKey` and `RGATreeSplitNodeID.cachedKey` →
      `atomic.Value` holding a string
- [x] Drop the `cachedActorIDBase64` cache: `ActorIDBase64` has no
      production callers, so it returns `actorID.StringBase64()` directly
- [x] Drop the `init()` warm-ups and the "not safe until warmed" doc comments
- [x] Confirm no value copies: both structs are shared by pointer only;
      `make lint` and `go vet ./...` are clean
- [x] Deep-equality guards for the warmed cache in `ticket_test.go` and
      `rga_tree_split_test.go`; Red against the `atomic.Pointer` draft
- [x] `go test -race ./pkg/... ./api/...` green
- [x] Benchmarks against main (benchstat, n=8, interleaved)

## Results

`unsafe.Sizeof` on arm64: `time.Ticket` 56 → 32 bytes,
`crdt.RGATreeSplitNodeID` 32 → 24 bytes. The benchmarks below were taken
with `cachedActorIDBase64` still in the struct, when `Ticket` was 40 bytes.
Dropping it only shrinks `Ticket` further, and nothing on the production
path calls `ActorIDBase64`.

benchstat geomean against main: allocs/op +0.68%, B/op −1.21%, ns/op +0.37%.
Insert paths and Array/random_delete show no change in allocs. Text-delete and
GC paths go up 2–3% in allocs, for the 16-byte `*string` box that `Store`
allocates the first time a fresh ID is keyed. B/op drops everywhere because
the structs are smaller.

Not run here: the server-side integration tests and benchmarks, which need
MongoDB.

## Known limitation

The integration lane fails on
`TestAuthWebhookPresenceOnly/a_reader_attaching_alone_keeps_the_bound_schema`
with `client epoch(1) != document epoch(0)`. The attach seeds the client's
epoch from an uncached `FindOrCreateDocInfo` read and `pushPack` checks it
against a `FindDocInfoByRefKey` read served from a per-node `docCache` that
another node's compaction does not evict. The lessons file traces it; the
reads and the eviction are all in `server/`, which this change does not
touch, so it is left for a separate fix.

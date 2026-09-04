---
title: offline-resumable-attach
target-version: unreleased
---

# Offline-Resumable Attach

## Problem

A client (e.g. Wafflebase) that edits while offline keeps its un-pushed
changes only in the SDK's in-memory `Document`. Closing or reloading the tab
loses that work. To make local persistence possible, the SDK needs to store a
document and its pending changes on disk and, on the next session, push them to
the server. Today the server prevents this in two independent ways.

**1. The actor is not stable across sessions.** `ClientInfo.ID` *is* the
actorID (both are 12-byte ObjectIDs; the server converts with
`types.IDFromActorID` / `cli.ID.ToActorID()` throughout). `ActivateClient`
mints a fresh `_id` on every call:

```go
// server/backend/database/mongo/client.go (ActivateClient)
// server/backend/database/memory/database.go (ActivateClient)
info := &database.ClientInfo{ ID: types.NewID(), Key: key, ... }
// InsertOne — no upsert, no reuse by key
```

Every position ticket embedded in a stored change (`fromPos`, `toPos`,
`parentCreatedAt`, element `createdAt`) carries the old actorID. Replaying those
changes under a new actor requires rewriting them — and that rewrite is
unsound (see [Why not rebase](#alternatives-considered) and the SDK-side
companion doc). A stable actorID makes replay correct **by construction**,
requiring zero ticket rewriting.

**2. Re-attach resets the checkpoint to zero.** Even with a stable actor,
`DetachDocument` (`server/backend/database/client_info.go`) and `TryAttaching`
reset `ClientSeq`/`ServerSeq` to `0`. On the next attach the server's checkpoint
is `0`, but the restored local changes start at, say, `clientSeq = 6`.
`pushpull` then rejects them: `validateClientSeqContinuity` expects the incoming
`clientSeq` to be exactly `checkpoint.ClientSeq + 1`. The un-pushed work is
refused.

The equation `actorID == clientID == checkpoint owner` is baked into the
protocol. Local persistence breaks it, so this is a protocol change, not a
documentation gap.

### Goals

- The same logical client resumes the **same actorID** across activate cycles,
  so locally-stored changes replay without ticket rewriting.
- On re-attach, a client can **resume its last checkpoint** instead of starting
  from `0`, so its persisted un-pushed changes push cleanly.
- No regression for existing clients that do not opt in (they still get a fresh
  actor per session).
- Keep the CRDT protocol sound: no change to how elements are keyed, ordered, or
  deduplicated.

### Non-Goals

- Client-side storage, serialization, and the restore flow — covered in the
  yorkie-js-sdk companion doc `offline-local-persistence.md`.
- Concurrent multi-tab editing under one identity. A single stable identity
  shared by two live tabs collapses to one checkpoint and corrupts `clientSeq`;
  this is bounded by a single-active-session lease on the SDK side, not solved
  here.
- Server-authored snapshots or any change to GC/compaction cadence.

## Design

Two additions, both opt-in and both keeping per-session client rows intact.

### 1. Stable actor, decoupled from the per-session client row

Do **not** make `ActivateClient` idempotent per `clientKey`, and do **not** try
to reuse the actor purely on the client. Both were evaluated and rejected:

- **Upsert-by-`clientKey`** (reuse `ClientInfo.ID`): `ColClients` is sharded on
  `[project_id, _id]` (`indexes.go`); a unique index on `{project_id, key}`
  cannot be enforced without a shard-key prefix, so upsert-by-key is racy on any
  sharded deployment and its enabling index fails to build against existing
  duplicate rows.
- **Pure client-side reuse** (SDK persists its actor and stamps it via
  `doc.setActor` after a fresh activation): the server persists a change's actor
  straight from the wire (`change_info.go:72` reads `c.ID().ActorID()`, decoded
  in `from_pb.go:249`) and **never validates it against `clientInfo.ID`**, so a
  divergent push is *silently accepted* — then breaks three server invariants
  (self-echo dedup, VV/GC keying, checkpoint continuity; see
  [Alternatives](#alternatives-considered)). Making it correct requires the exact
  same server changes as the decoupled field below, so client-side-only reuse
  buys nothing and corrupts state in the meantime.

Instead, **decouple a stable actor identity from the per-session session
identity**. The change localizes to two seams. The hard invariant tying them
together: *the actor stamped into persisted changes must equal the key used for
pull dedup, VV, min-VV, and GC.* Both are `ClientInfo.ID` today; move **both**
to the stable actor together — never one without the other, or GC advances past
un-synced tombstones.

#### Seam 1 — actor identity on the wire

- `ClientInfo.ID` stays a fresh per-session ObjectID. It remains the **session
  id** used for every RPC row lookup (`types.IDFromActorID(actorID)` →
  `ClientRefKey` in `yorkie_server.go`, ~21 sites) and the shard key. Do **not**
  feed the stable actor into those lookups.
- Add a stable `StableActorID` field to `ClientInfo` (`client_info.go`), derived
  deterministically as `StableActorID = DeriveActorID(project_id, clientKey)` at
  `ActivateClient` (see [Stable actor derivation](#stable-actor-derivation)).
  Deterministic derivation means two concurrent activations for the same key
  compute the same actor with **no** unique index and **no** upsert. It is a plain
  field, never a Mongo `_id`, so it carries **no** unique index — the same key
  across sessions legitimately shares one actor.
- Return it **additively** in `ActivateClientResponse` (`api/yorkie/v1/yorkie.proto`;
  the request already carries `client_key`). Old SDKs ignore the new field and
  keep using `client_id` as their actor (status quo). The SDK splits `this.id`
  (`client.ts:547`) into a *session id* (wire `clientId`, all RPCs) and a
  *stable actor* (fed only to `doc.setActor`), with a nil-guard fallback to
  `clientId`-as-actor against old servers.

#### Seam 2 — dedup and VV liveness

Switch these from `clientInfo.ID` to the stable actor:

| Site | Role |
|------|------|
| `pushpull.go:568` (`clientInfo.ID == pulledChange.ActorID`) | Self-echo dedup — **the single most important switch** |
| `pushpull.go:163` | Pubsub publisher actor |
| `pushpull.go:377` | `DisableGC` VV truncation key |
| `clients.go:90` | Cluster `DetachDocument` actor |
| `client_info.go:354/360` | `VersionVectorInfo.ClientID` construction |
| `memory/database.go:2260-2296`, `mongo/client.go:2374-2475` | VV upsert, min-VV, delete-on-detach, vector cache |

Sites that **stay** on the session `_id`: all `IDFromActorID` RPC row lookups;
`ActivateClient` minting a fresh `_id`; housekeeping row reaping; checkpoint
ownership in `ClientDocInfo` (scoped per session on purpose — see below);
analytics counting (Q4). Snapshot authoring uses `InitialActorID`, unaffected.

A client that does not supply a stable identity keeps today's behavior: a random
actor per session.

#### Stable actor derivation

An actor is exactly **12 bytes** (`time.ActorID [12]byte`; `types.ID` is its
lowercase-hex form). The derivation is server-authoritative — the SDK receives
the result and stamps it verbatim, so it never recomputes the hash:

```
DeriveActorID(projectID types.ID, clientKey string) time.ActorID:
    tag = "yorkie/stable-actor/v1"                  // domain tag, permanent
    h   = SHA256(tag || projectID.Bytes()/*12B*/ || []byte(clientKey))
    a   = h[0:12]
    for a == time.InitialActorID || a == time.MaxActorID:   // reserved-value guard
        h = SHA256(tag || 0x01 || h); a = h[0:12]
    return time.ActorIDFromBytes(a)                 // → .String() = lowercase 24-hex
```

Design points:

- **Plain SHA256, not HMAC.** `clientKey` is already the client's own credential;
  forging another client's actor requires knowing that client's key regardless, so
  a server secret adds little. It would also have to be stable and shared across
  every node forever (rotation breaks all actors) — an operational liability the
  plain hash avoids. Determinism is then free: identical on every node, restart,
  and offline.
- **`projectID` first, fixed 12-byte width**, so concatenation with the
  variable-length `clientKey` is unambiguous; including it namespaces the actor
  per project.
- **Reserved-value guard.** Hitting all-zero (`InitialActorID` / system client) or
  all-`0xFF` (`MaxActorID`) has probability ≈ 2·2⁻⁹⁶; the loop makes the fallback
  deterministic anyway.
- **`clientKey` is used byte-for-byte** — no trimming or case normalization, or
  two encodings would derive different actors. Apps already must send a
  byte-identical key each session for a stable identity.
- **The `v1` tag is permanent.** Changing the algorithm changes every actor and
  orphans every persisted offline document; bump the version only in an emergency.

Collision profile: 96 uniform bits. Per-project birthday collision for N distinct
keys ≈ N²/2⁹⁷ (~6×10⁻¹² at N = 10⁹) — the **same** profile the system already
accepts for random 12-byte ObjectID actors, so derivation is no worse. There is no
DB uniqueness check (there cannot be), so a collision would be silent; the 96-bit
width is the mitigation.

Encoding matches existing actors exactly (lowercase hex via `.String()`), so the
derived actor slots into the current comparison path unchanged. (Note a
pre-existing cross-runtime detail, unrelated to this change: Go compares actors by
raw bytes while the JS SDK compares by `localeCompare` on the hex string; identical
lowercase-hex encoding keeps them in agreement, as it already does for random
actors.)

### 2. Resumable checkpoint on attach

A stable actor alone does not enable replay: on attach, `ClientDocInfo`
checkpoint starts at `0` while the restored local changes continue the actor's
`clientSeq` lineage. Two mechanisms close this, both reusing existing machinery.

#### Conditional checkpoint reset (Q3)

Make the reset-to-`0` conditional at exactly two owned sites; leave
`validateClientSeqContinuity` (`pushpull.go:205`) as the loud safety net.

1. `ClientInfo.AttachDocument` (`client_info.go:154-159`): **preserve** existing
   `ServerSeq`/`ClientSeq` when the same stable-actor row already holds a
   `ClientDocInfo` for `docID` in `Attached`/`Attaching` status (crash/reload
   resume); otherwise zero as today. Thread an explicit **resume-intent flag**
   (or `change.Checkpoint`) into the pure-struct method rather than reading
   global state, to keep it testable.
2. Both `TryAttaching` impls (`memory/database.go:1064-1068`,
   `mongo/client.go:1112-1113`): stop clobbering `server_seq`/`client_seq` on the
   `attached→attaching` transition, letting `AttachDocument` be the sole owner.

Do **not** touch `DetachDocument`/`RemoveDocument` resets — a genuine detach must
restart `clientSeq` at `1`. The current `IsAlreadyDetached` guard
(`clients.go:160-163`) blocks re-attach today, so the resume path is opened
deliberately via the resume-intent flag, not by relaxing the guard.

#### Pull-before-trust reconciliation (Q2)

Never trust the presented `serverSeq` blindly. Reuse the **epoch +
snapshot-threshold + `FindClosestSnapshotInfo` empty-fallback** machinery (see
`document-epoch.md`) as a three-tier contract — do **not** build a parallel path:

- **Tier 1** — same epoch, `serverSeq` behind, rows intact: normal `PushPull`
  auto-ships a snapshot re-anchor via `preparePack → pullSnapshot`
  (`pushpull.go:447/470`) when `(initialServerSeq − client.ServerSeq) ≥
  snapshotThreshold`.
- **Tier 2** — epoch advanced by force-compaction, or GC'd past the client
  `serverSeq`: warm `PushPull` returns `ErrEpochMismatch` (`pushpull.go:88/431`);
  on re-attach the checkpoint resets to `0` with the current epoch, and
  `FindClosestSnapshotInfo` returns an empty snapshot at seq `0`, so re-anchor
  always succeeds while the `DocInfo` row survives. Reuse `ErrEpochMismatch` as
  the "re-anchor by re-attach" signal — do not invent a new one.
- **Tier 3** — unrecoverable (`DocInfo` purged): there is **no server signal**;
  attach silently mints a fresh empty doc via `FindOrCreateDocInfo`. This is
  closable only on the client (persist `docID`/`epoch` with the offline
  snapshot; raise a data-loss event on re-attach when the returned `docID`
  differs or `serverSeq` regressed to `0` against a non-empty local snapshot).

### Data flow

```
attach(docKey, sessionId, resumeIntent?, lastCheckpoint?)
  └─ server resolves StableActorID = derive(project_id, clientKey)  [Seam 1]
  └─ AttachDocument: resumeIntent && existing ClientDocInfo[docKey] Attached/Attaching
       ? preserve ClientSeq/ServerSeq                               [Q3]
       : checkpoint = 0 (fresh)
  └─ pull-before-trust: epoch/serverSeq behind → snapshot re-anchor [Q2 tiers]
  └─ client pushes pending changes from the (preserved) clientSeq
       └─ validateClientSeqContinuity guards continuity (loud on mismatch)
  └─ dedup/VV/min-VV keyed on StableActorID                         [Seam 2]
```

### Analytics invariants (Q4)

The decoupled form keeps per-session rows, so `CountActivatedClients`
(`memory/database.go:1699`, `mongo/client.go:1742`) and `GetProjectStats`
(`projects.go:134`) are preserved. Two invariants must be recorded so a later
refactor does not silently break them:

- **`client_events` must keep emitting the per-session row id (`cli.ID`),
  never the stable actor** (`yorkie_server.go:83`). The warehouse active-clients
  metric counts distinct `client_id`; substituting the stable actor collapses
  its cardinality to distinct actors.
- `CountActivatedClients` / `GetProjectStatsCounts` now count **sessions, not
  unique actors** — a semantic note, no code change.
- `session_events.user_id` (`channel/manager.go:696`) will carry the stable
  actor post-change but is **not** a count key (session metric counts
  `session_id`); cosmetic — leave it or switch to the row id for consistency.

### Migration

Purely additive at the DB layer. The proto gains an `ActivateClientResponse`
field (additive, safe). The VV re-key writes new `VersionVectorInfo` rows under
the stable actor; because VV rows are deleted on detach, a rolling deploy needs
**no data migration** (new writes use the new key). The one transition hazard: a
long-lived attached doc spanning the deploy could briefly hold mixed-keyed VV
rows — confirm min-VV cannot regress GC during that window, or drain/migrate
docs attached across the upgrade.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Seam 2 must move dedup **and** VV keying together; moving only one lets min-VV compare mismatched key spaces → GC past un-synced tombstones (data loss) | Treat "change actor == dedup/VV/GC key" as a hard invariant; switch all Seam-2 sites in one change; land behind an opt-in path so default behavior is byte-identical |
| Housekeeping (`FindDeactivateCandidates`, `housekeeping.go`) reaps a client idle past `ClientDeactivateThreshold`; a later return must revive under the same stable actor | Deterministic derivation reproduces the actor regardless of row lifecycle; revive reconciles via pull-before-trust |
| A resumed checkpoint is behind `serverSeq` after compaction/purge | Pull-before-trust re-anchors via the three-tier contract before accepting pushes; never trust the stored `serverSeq` |
| Tier 3 (`DocInfo` purged) has no server signal — attach silently mints a new empty doc | Closed only on the client: persist `docID`/`epoch` and raise a data-loss event on mismatch. Future server hardening: expose a distinguishable "purged" signal |
| Two live tabs share one stable actor → one checkpoint → `clientSeq` collisions and self-filtered pull dedup (real edit loss) | Bounded by the SDK single-active-session lease; background tabs observe, do not drive sync |
| `Database.TryAttaching` signature change (resume flag) ripples to both backends and all test doubles | Contained interface change; covered by existing attach test suites plus new resume-path tests |
| VV re-key mixed-keying window on rolling deploy | See [Migration](#migration); confirm min-VV cannot regress during the transition |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Decouple a stable `StableActorID` from `ClientInfo.ID` rather than upsert-by-key or client-side reuse | Upsert's `{project_id, key}` unique index is unenforceable on the sharded `ColClients`; client-side reuse is silently accepted by the server but breaks dedup/VV/checkpoint and needs the same server work anyway |
| Derive the actor deterministically via plain `SHA256(tag \|\| projectID \|\| clientKey)[:12]` | No unique index, no upsert, no cross-session write coordination; plain hash (not HMAC) needs no shared secret and is identical on every node/restart/offline; concurrent activations converge on the same actor |
| Return the stable actor additively in `ActivateClientResponse`; keep `clientId` = session `_id` on the wire | All RPC row lookups stay unchanged (~21 `IDFromActorID` sites); old SDKs keep working by ignoring the field |
| Keep per-session client rows | Preserves checkpoints, housekeeping, and activation metrics; isolates the change to the actor-identity layer |
| Resume checkpoint conditionally in `AttachDocument`, guarded by `validateClientSeqContinuity`; drop the `TryAttaching` seq resets | Un-pushed local changes cannot push from a zeroed checkpoint; a single owner site plus the existing continuity guard turns a bad resume into a loud error, not corruption |
| Reuse epoch + snapshot machinery for pull-before-trust; reuse `ErrEpochMismatch` | The stale-after-compaction recovery path already exists; a parallel path would duplicate and drift |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| `ActivateClient` upsert-by-`clientKey` (reuse `ClientInfo.ID`) | Unique index on `{project_id, key}` unenforceable on the sharded `ColClients`; racy inserts; index build fails against existing duplicate rows; two tabs sharing a key collapse to one checkpoint |
| Pure client-side actor reuse (SDK persists its actor, no server change) | The server persists a change's actor from the wire (`change_info.go:72`) and never checks it against `clientInfo.ID`, so a divergent push is silently accepted — then breaks (1) self-echo dedup (`pushpull.go:568` goes false → own changes echo back and re-apply; non-idempotent ops like `IncreaseOperation` double-count), (2) VV accounting (VV rows keyed by session id, vector entries by the persisted actor → min-VV mixes key spaces → GC past un-synced tombstones), (3) checkpoint continuity (attach zeros seqs while the restored doc continues the persisted actor's `clientSeq`). Fixing any of these requires the same server changes as the decoupled field |
| Client-side rebase on restore (SDK gets a new actor and rewrites pending tickets) | The pushed/pending boundary is per-embedded-ticket, not per-change; a normal edit references already-pushed elements under the old actor, forcing mixed-actor changes that miss the actor-keyed element map (`root.go`, `Ticket.Key()`) or corrupt the actorID tie-break ordering. The only discriminator (a lamport watermark) is unsound: one change shares one lamport and lamport is bumped non-monotonically on every pull. Failure mode is silent divergence. Detailed in the SDK companion doc |
| Keep the checkpoint reset and re-push everything from `0` | `validateClientSeqContinuity` rejects changes whose `clientSeq` does not continue from the checkpoint; also re-uploads already-acked work |

## Open Questions

- **VV re-key transition window.** Whether a one-time migration or drain is
  needed for docs attached across the rolling deploy (see [Migration](#migration)).
- **`TryAttaching` signature.** Resume-intent flag vs `change.Checkpoint`; and
  whether the mongo atomic `FindOneAndUpdate` can drop the seq `$set` lines
  without losing the attaching-status transition guarantee.
- **Tier 3 purge signal.** Whether the server should eventually expose a
  distinguishable "document purged" signal on attach (future hardening).

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents. See
the yorkie-js-sdk companion design `docs/design/offline-local-persistence.md`
for the client-side storage, serialization, restore flow, and multi-tab lease
that this protocol enables.

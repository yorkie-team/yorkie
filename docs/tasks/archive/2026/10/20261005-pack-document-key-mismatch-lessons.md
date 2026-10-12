**Created**: 2026-10-05

# Reject Change Packs Naming Another Document — Lessons

Plan: `20261005-pack-document-key-mismatch-todo.md`.

## Where the check goes

All three handlers write only in `packs.PushPull`, after
`documents.FindDocInfoByRefKey`, and the lookup is already there to read
`disable_presence`. Comparing right after it costs nothing extra and stops
the request before any write. Earlier steps only read (client lookup,
`IsDocumentAttachedOrAttaching`) or take locks; the locks were taken on the
pack's key, which is the right key once the check passes.

Checking before `VerifyAccess` would need the lookup before authorization;
nothing gains from that, since the webhook answer is discarded on mismatch.

`AttachDocument` has no `DocumentId`; it finds or creates the document by the
pack's key, so it has nothing to compare. The revision handlers already build
their webhook attributes from `docInfo.Key`.

## Proving the test

Without the check, each subtest fails: PushPull and Detach apply the pack's
`{"x":1}` to the target, and Remove removes it. The first version shared one
target across the subtests, and without the check the later requests hid the
earlier one's write (the final `{}` passed by accident), so each subtest now
uses its own pair of documents and checks its own target.

## Self-review (round 1, correctness/tests)

- The SDKs always send the attached document's own key in the pack, so a
  legitimate client never hits the new error.
- `docInfo` comes from the LRU cache by ref key; a document's key never
  changes, so a cached entry is as good as a fresh read.

No blocking findings.

## Review panel (round 1 on 3cc65c61), addressed in the next commit

The panel approved with suggestions. Taken:

- **Cluster `DetachDocument`** (correctness, security, design-fit,
  blast-radius) takes `DocumentId` and `DocumentKey` too and pushes a pack
  built from the key. The server's only caller is `clients.Deactivate`, which
  always sends a matching pair, but the service is gated by `ClusterSecret`
  only when one is set: it is empty by default, and then the cluster
  interceptor accepts any caller (see #2113). So the check is reachable from
  outside in the default configuration, and the invariant should hold on
  every path that pairs the two.
- **Tests** (correctness, test-adequacy): each subtest now also sends the
  same request with the target's own key and expects it to succeed, which
  covers the guard not misfiring. The RemoveDocument subtest's "target is
  `{}`" check proved nothing (a removed document re-attaches empty); it now
  relies on the matching-key removal succeeding, which fails if the
  mismatched one had removed the target. A cluster subtest was added.

Not taken, with reasons:

- **Locks keyed on the pack key before the check** (correctness, security,
  design-fit nit, blast-radius): the check runs before any write, and once it
  passes the pack key is the document's key, so the locks are the right ones.
  A mismatched request only holds the other document's locks until it is
  rejected. Moving the check before the locks needs the lookup before them;
  it buys nothing for correctness.
- **Distinct error as a document-ID existence oracle** (security): PushPull
  checks attachment to `DocumentId` before the guard, and Detach/Remove
  already answer `ErrDocumentNotFound` for unknown IDs, so an attached client
  learns at most that an ID it sent exists in its own project. Folding the
  mismatch into not-found would hide a client bug behind a misleading error.
- **Enforce once inside `packs.PushPull`** (design-fit): `PushPull` looks the
  document up inside `pushPack`, under the push lock, as part of writing. The
  handlers are where the two identifiers enter from a request, so the check
  sits at that boundary, next to the lookup the handlers already do.
- **`verb` for removals** (relocated): handled by #2129.

## Review panel (round 2 on 9a0f79ec)

Approved. Fixed: the task records called the cluster handler cluster-secret
gated, which is false by default, and still said "three handlers". Not taken:
`PurgeDocument` and `CompactDocument` on the cluster service also pair an ID
with a key, but they are housekeeping/admin operations that never reach the
auth webhook with the key, so they are outside this PR's invariant and are
left for #2113, which covers the unauthenticated cluster service. The
lock-order and existence-oracle points were answered in round 1.

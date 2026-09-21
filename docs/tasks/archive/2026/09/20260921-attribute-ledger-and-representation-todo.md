# The attribute ledger, and which SDK's representation is canonical

**Created**: 2026-09-21

Closes #2007, #2002 and #2003. Written retrospectively — the plan file should
have come first and did not, which is itself recorded in the lessons.

Three issues that read as separate turned out to be one ledger with four leaks
in it, plus a representation split underneath them. The forced order in #2002's
own comment held exactly: **#2007 first, then this becomes a two-line change
with nothing to reconcile.**

## Goals

- #2007: a tombstoned text attribute leaves Live and is not stranded there when
  it is purged, matching what `TreeNode.DataSize` already did.
- #2002, text half: the attribute tombstones a split copies are registered, so
  they can be collected.
- #2003: the same document reports the same size on both SDKs.
- Both SDKs, because the size limit is enforced client-side against each one's
  own accounting.

## Non-Goals

- **Styling a tombstoned node.** `canStyle` admits a removed node while the
  container's `DataSize` skips it. This work stops that drifting Live on both
  the text and the tree paths, but the underlying behaviour is a cross-SDK
  convergence divergence — the JS SDK skips removed nodes outright where Go
  styles them, so the same history leaves restored text styled on one side and
  unstyled on the other. That is a CRDT semantics decision, not an accounting
  one. The GC half of it is untouched and asserted as such.
- **`Document.applyChange`'s clone-then-root order.** Any operation that throws
  mid-execute leaves the two permanently out of step while `getRoot()` reads
  the diverged clone. Measured at root=3 / clone=5 nodes from one poisoned
  pack. A general corruption vector this work merely exposed.
- **`applyChangePack`'s checkpoint on a failed change.** It does not advance,
  which turns any single poisoned change into infinite redelivery. Advancing
  past a change you could not apply is also wrong; this needs its own decision.
- **A value-kind field on the wire.** It is the only thing that would close the
  last representation case, and it is a protocol change on both SDKs and the
  server that still leaves a default for every value already stored.

## What was actually wrong

Four defects in one ledger. `docSize.Live` is a running accumulator, never
recomputed, so it cannot detect its own drift — every number below comes from
comparing it against a document rebuilt from the same content.

| defect | in the issues? |
|---|---|
| a text attribute tombstone charged to Live and GC at once | #2007 |
| purging it left its bytes in Live forever | #2007 |
| **every attribute overwrite leaked one attribute, tree and text** | no |
| **a write that lost LWW still charged Live** | no |

The two unreported ones came out of the audit #2007 asked for rather than the
one-line fix it warned against. The overwrite leak is the one that matters in
practice: it fires on the most common operation a rich-text editor performs.

`RHT.Set` now reports what it did — installed, revived, superseded — and every
call site books from that rather than reading the map afterwards. That one
change closes three of the four and removes the need for the `index.End` guard
on the tree's add.

## #2003 was not what it said

Filed as a sizing divergence. Reproduced end to end, it is an interop break
with an unrecoverable crash: a Go client writing `color="red"` made every JS
client throw `SyntaxError` inside `applyChangePack`, **before the checkpoint
advances**, so the server redelivered the same change forever and the document
could never be opened. Snapshot load did not throw — the raw bytes go straight
into the RHT — so a client could attach successfully and die on first render.

Three decode sites needed the guard, not one. The fallback is the raw string,
which is exactly what the peer wrote.

The sizing had two independent causes stacked: the JSON quotes, and `.length`
counting UTF-16 units where Go's `len()` counts UTF-8 bytes. The gap did not
even have a consistent sign — `color="red"` made JS 4 bytes heavier and
`color="빨강"` made it 4 lighter.

And the representation question the issue says it was filed for is now
answered, toward this SDK: an ordinary string is stored as itself on both
sides. Only a string that is itself a JSON document keeps its quotes, because
stored raw it could not be told from the value it encodes.

## Verification

| | |
|---|---|
| randomized differential, attribute-only, 720 steps | 333 Live divergences → **0** |
| Go suite | 0 failing, `make lint` 0 issues |
| JS suite | 538 passing, `tsc` 0 errors, eslint clean |

Every fix was checked by reverting only its production file and confirming the
test fails. The #2003 tests were run against `origin/main` and fail there.

## Release constraint

The JS change alters what goes on the wire. A client without the tolerant
decode throws on a raw value, so a mixed-version JS fleet is exposed until
every client has the release — the same wedge this work removes for
Go-authored values, pointed the other way. Flagged at the top of the PR.

## See Also

- `docs/tasks/archive/2026/09/20260920-post-gc-panic-and-snapshot-tombstone-todo.md`
  — #2006 and #2008, whose review turned up the defects this task fixes

# Tree Convergence Conformance Suite

A serverless suite that replays the same step scripts in the Go SDK (this
package) and the JS SDK (`packages/sdk/test/conformance/` in
yorkie-js-sdk). Two runners that follow this document produce byte-identical
records for the same script, so Go/JS parity is itself a check.

This file is the normative spec of format v1 and the SSOT for both runners.
yorkie-js-sdk keeps a copy of `tree/` with a `SOURCE` file holding the yorkie
commit it came from. Where this spec is silent, the behaviour of the scratch
fuzz `tree_1436_fuzz2` (Go and TS twins) is the reference.

```sh
go test ./test/conformance/... -count=1                         # corpus + parity
CONFORMANCE_UPDATE=1 go test ./test/conformance/ -run TestParity # rewrite fixtures
CONFORMANCE_FAM=all CONFORMANCE_RUNS=5000 CONFORMANCE_OUT=/tmp/all.json \
  go test ./test/conformance/ -run TestLargeRun -v               # large run
```

## Script

```json
{
  "v": 1,
  "id": "js1436-min-a",
  "issues": ["yorkie-js-sdk#1436", "yorkie#2167"],
  "status": { "go": "known-diverge", "js": "known-diverge" },
  "note": "free text",
  "replicas": 2,
  "init": "abcd",
  "steps": [ ... ]
}
```

- `init`: the text of the single paragraph the document starts with:
  `<doc><p>{init}</p></doc>`. Replica 0 creates the tree with
  `root.t = new Tree({type:'doc', children:[{type:'p', children:[{type:'text', value: init}]}]})`
  and every other replica applies that change before step 0. The init change
  is not in the server log. Its push ack and its delivery carry the initial
  (empty) version vector in both GC modes.
- Actor of replica `i` is the 24-digit decimal `i+1` zero-padded
  (`000000000000000000000001`), as in fuzz2. Document key `conformance`.
- `status` per SDK, the ledger: `pass`, `known-diverge`, `known-error` or
  `known-fail`, expecting the result `pass`, `diverged`, `error` or `fail`
  respectively. Only corpus scripts carry it.
- Loading is strict and fails the script, naming its file, on: input other
  than whitespace after the script; an unknown field (top level or in a
  step); a `v` other than 1; a `replicas` that is not a positive integer; a
  `status` key other than `go` and `js` or an unknown `status` value; a
  step without `r` or `op`, or with an `r` that is not an integer in
  `0..replicas-1`; an unknown `op`; a step missing a field its op requires,
  or carrying a field its op does not take (even one valid for another
  op); a `null` field; or a field of the wrong type (see Steps).

## Steps

Each step has `r` (replica index, an integer) and `op`, and exactly the
fields of its op below: required unless marked optional, none other.
`from`, `to` and `split` are integers.

| op | fields | effect |
|---|---|---|
| `edit` | `from`, `to`, optional `text` (string), optional `split` (int) | `t.edit(from, to, text ? {type:'text', value:text} : undefined, split ?? 0)` |
| `style` | `from`, `to`, `attrs` (non-empty object of string→string) | `t.style(from, to, attrs)` |
| `removeStyle` | `from`, `to`, `keys` (non-empty array of strings) | `t.removeStyle(from, to, keys)` |
| `undo` / `redo` | – | `doc.history.undo()` / `redo()`; when the stack is empty it is a noop |
| `pull` | – | deliver the next server-log entry this replica has not seen (see Delivery). Noop if none |
| `sync` | – | `pull` until none is left |
| `snapshot` | – | replace replica `r` by a document rebuilt from its own snapshot bytes (see Snapshot) |
| `gen` | `k` (`ins`, `split`, `del`, `merge` or `style`), `f`, `g`, `h` (numbers in [0, 1)), `v` (string) | abstract generator event, resolved on replica `r` exactly as fuzz2 resolves `ins`, `split`, `del`, `merge`, `style` |

An empty `text` counts as absent.

Every step that is a local operation (`edit`, `style`, `removeStyle`,
`undo`, `redo`, `gen`) appends exactly one entry to the server log at that
step, authored by `r`, carrying the changes it produced. If it produced no
change (generator noop, empty undo stack), the entry carries no changes but
is still appended and still counts for delivery — this keeps fuzz2's
numbering.

An exception thrown by any step aborts the run with `error` (I2 failure).

### `gen` resolution (fuzz2, verbatim)

Let `ps` be the live paragraphs of replica `r`: from `t.toJSON()`, each child
of the root gives `[base, text]` where `text` is the concatenation of its
text children and `base` starts at 0 and advances by `len(text) + 2`.

`len` counts UTF-16 code units, the unit of Tree indexes in both SDKs (a JS
string's `length`; Go encodes the text with `utf16.Encode`), so every base,
slot, candidate and room below is in UTF-16 code units. A slot or candidate
inside a surrogate pair is not skipped: the Tree rejects such an index in
both SDKs, so the step throws and the run ends with `error`. The generator
only writes ASCII, so this needs a hand-written `init` or `text` with
characters outside the BMP.

- `ins` / `split`: slots = for each paragraph, `k = 0..len(text)`, index
  `base + 1 + k`. Pick `slots[floor(f * len(slots))]`. `ins` →
  `edit(idx, idx, text=v)`; `split` → `edit(idx, idx, split=1)`.
- `del`: candidates = for each paragraph, `k = 0..len(text)-1`, index
  `base + 1 + k`, room `min(3, len(text) - k)`. Pick by `f`;
  `len = 1 + floor(g * room)`; `edit(idx, idx + len)`.
- `merge`: needs ≥ 2 paragraphs. `i = floor(f * (len(ps) - 1))`;
  `a = floor(g * (min(2, len(text_i)) + 1))`;
  `b = floor(h * (min(2, len(text_{i+1})) + 1))`;
  `from = base_i + 1 + len(text_i) - a`; `to = base_i + len(text_i) + 3 + b`;
  `edit(from, to)`.
- `style`: needs ≥ 1 paragraph. Pick paragraph by `f`; key `b` if `g < 0.5`
  else `i`; `style(base, base + len(text) + 2, {key: v})`.
- No candidate → noop.

## Delivery and GC

- Server log: ordered entries `{author, changes?}`. Server order is the
  order steps appended them; delivery is causal by construction.
- Every pack a replica applies goes through the protobuf converter
  (`toChangePack` → `fromChangePack`): the push (`createChangePack`), the
  push ack, each pulled entry (a pack of that entry's changes with
  `Checkpoint(0, 0)`) and the final GC pack.
- Push ack: after a local op that produced changes, the author applies an
  empty pack with `Checkpoint(0, lastClientSeq)`.
- `pull` for replica `r`: skip entries authored by `r`; if an entry is left,
  apply its pack (if it has changes) and advance. One `pull` delivers one
  entry.
- Two GC modes, both run for every script:
  - `off`: every applied pack carries the initial version vector. No GC.
  - `minvv`: the fake server keeps one row per replica. All rows are set
    right after the init change, to each replica's document version vector.
    The author's row is set to its version vector right after a local op
    that produced changes (before the ack); the puller's row is set right
    after it applies a pulled entry with changes. `minVV` is the
    element-wise minimum over all rows (an actor missing from a row counts
    as 0). A pack applied to replica `r` carries `minVV` only when, after
    that pack, the log holds no entry `r` has not seen and did not author;
    otherwise it carries the initial version vector. The real server
    answers a pull with every pending change before the client collects, so
    a tombstone is never collected while a concurrent change that refers to
    it is still on its way; one-entry pulls with `minVV` would collect it
    and fail with "node not found" errors the protocol cannot produce. GC
    happens only through `applyChangePack`.
- End of run, in each mode: for `r = 0..n-1`, `sync(r)` (not recorded as
  step lines). In `minvv` mode, then apply one more empty pack with
  `Checkpoint(0, 0)` and the final `minVV` to every replica.

## Snapshot

Rebuilding replica `r`: encode its root object with no presences using the
SDK's snapshot encoder (Go: `converter.SnapshotToBytes(root, nil)`), wrap them in a pack with `Checkpoint(0, lastClientSeq of r)`,
no changes, and `r`'s current version vector, send it through the
converter, and apply it to a new document with the same key and actor. The
rebuilt document has empty undo/redo stacks, and its local clientSeq starts
over from 0, while a real client keeps counting from where it was. Both
runners do this and nothing in a record depends on clientSeq, so parity
holds; but a corpus script should not rely on local edits after a
`snapshot` behaving like a real client's as far as clientSeq goes.

A `snapshot` step replaces replica `r` with its rebuild in both modes; in
`off` mode it also checks I3 on it. Cursor and server row are unchanged.

## Checks

Run the script in `off` mode, then again in `minvv` mode. If `off` mode
throws, `minvv` mode is not run.

- **I1** (off mode): all replicas have equal `toXML()` and equal shape.
- **I2**: no step threw (either mode).
- **I3** (off mode, at every `snapshot` step and, after the end-of-run
  drain, for every replica): the rebuild gives the same `toXML()` and shape.
- **I4**: final `toXML()` of replica 0 in `minvv` mode equals off mode, and
  all replicas agree in `minvv` mode.
- **I6**: not implemented in v1.

A check that could not be evaluated because a run threw is `false`, and a
check that throws counts as `false`: a rebuild that throws in the I3 check
fails I3 (result `fail`) instead of aborting the runner.
`diverged` = I1 fails. A run's result is `pass` only if I1–I4 all hold.

## Canonical strings

- Shape (off mode, includes tombstones), walking `allChildren` / the Go
  equivalent (`Children(true)`) in stored order:
  - id: `{lamport}:{actorHex}:{offset}` from the node's createdAt and offset,
    built from fields, not from an SDK `toString`.
  - text: `{id}{x?}"{value}"` (value unescaped); element:
    `{type}#{id}{x?}[{child},{child}…]`; `x` when removed.
- Step description (one line per step, in step order, from the `off` run):
  - local op that produced a change: `d{r+1}.edit({from},{to},'{text}')`,
    `d{r+1}.split({from})` for `split` without text and `from == to`,
    `d{r+1}.edit({from},{to})`, `d{r+1}.style({from},{to},{k}={v},…)` keys
    sorted, `d{r+1}.removeStyle({from},{to},{k},…)` keys in script order,
    `d{r+1}.undo`, `d{r+1}.redo`. A `gen` step is described by what it
    resolved to: `ins` as `edit(i,i,'v')`, `split` as `split(i)`, `del` and
    `merge` as `edit(from,to)`, `style` as `style(from,to,k=v)`.
  - local op with no change: `d{r+1}.noop`.
  - pull: `pull d{r+1} <- #{logIndex}` or `pull d{r+1} -` when nothing was
    left. `sync` expands to one `pull d{r+1} <- #{logIndex}` line per
    delivered entry and adds nothing when none was left. `snapshot`:
    `d{r+1}.snapshot`.
  - A step that throws adds no line.

## Record

```json
{
  "id": "…", "n": 2,
  "result": "pass | diverged | error | fail",
  "checks": { "I1": true, "I2": true, "I3": true, "I4": true },
  "steps": ["d1.edit(2,2,'c')", "pull d2 <- #0", "…"],
  "xml": ["<doc>…</doc>", "<doc>…</doc>"],
  "digest": "first 16 hex of sha256(steps.join('\n') + '\n' + xml.join('\n') + '\n' + shapes.join('\n'))",
  "xmlDigest": "first 16 hex of sha256(steps.join('\n') + '\n' + xml.join('\n'))"
}
```

`xml` and shapes are those of the `off` run after the end-of-run drain.

`result`: `error` if I2 failed, else `diverged` if I1 failed, else `fail` if
I3 or I4 failed, else `pass`. On `error`, `xml` is empty and both digests
are the first 16 hex of `sha256(steps.join('\n') + '\nERROR')` (error
messages differ between SDKs).

## Generator

mulberry32 exactly as fuzz2 (`gen(seed, fam, n, steps)`), with the same
families, weights, pull probability (0.5 when the replica has a pending
entry), value pool and pending count. Output is a script whose steps are
`gen` and `pull`, with `init` `abcd`.

| Fixture family | fam | n | steps |
|---|---|---|---|
| `is` | is | 2 | 24 |
| `del` | del | 2 | 24 |
| `merge` | merge | 2 | 24 |
| `style` | style | 2 | 24 |
| `all` | all | 2 | 24 |
| `all3` | all | 3 | 30 |

## Files

`tree/`:

- `corpus/*.json`: scripts with `status`. The runners are strict: the
  result must be exactly the one the status expects. When a change moves a
  script's result (a fix makes it pass, or it starts failing differently),
  update its `status` in the same change.
- `parity/{family}.json`: `{fam, n, steps, seed0: 1, runs: 200, diverged,
  rows: [{seed, result, digest, xmlDigest}]}` generated by the Go runner.
  `diverged` counts rows whose `result` is `diverged`.

Both runners check the parity fixtures: the header (`fam`, `n`, `steps`,
`seed0`, `runs`, `diverged`) must equal the regenerated one, `rows` must
hold `runs` rows, and every row's `result`, `digest` and `xmlDigest` must
match. The Go runner regenerates them with
`CONFORMANCE_UPDATE=1`; a change that moves a row regenerates the fixture in
the same commit, so the diff shows which seeds moved.

Env for large runs (no fixture comparison): `CONFORMANCE_FAM` (a fixture
family name), `CONFORMANCE_RUNS` (default 5000), `CONFORMANCE_SEED0`
(default 1), `CONFORMANCE_OUT` (writes the rows in the parity-fixture shape).

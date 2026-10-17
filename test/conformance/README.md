# Tree Convergence Conformance Suite

A serverless suite that replays the same step scripts in the Go SDK (this
package) and the JS SDK (`packages/sdk/test/conformance/` in
yorkie-js-sdk). Two runners that follow this document produce byte-identical
records for the same script, so Go/JS parity is itself a check.

This file is the normative spec of format v2 and the SSOT for both runners.
Format v1 is the subset of v2 without the features marked "v2"; a v1
script that uses a v2 feature is rejected. Moving the runners to v2 left
every existing record unchanged except `checks`, which gained E. Two v2
rules also apply to v1 scripts but changed none of them: the undo stacks
are cleared after the init change, and a `split` of level 2 or more has its
own step line.
yorkie-js-sdk keeps a copy of `tree/` with a `SOURCE` file holding the yorkie
commit it came from. Records are those of the rules in
`docs/design/concurrent-merge-split.md`; a yorkie change that moves them
under a replicated rule ships together with the matching yorkie-js-sdk
change (see that document's Rollout and Cross-implementation section), and
the JS runner reports the mismatch until then. Where this spec is silent,
the behaviour of the scratch fuzz `tree_1436_fuzz2` (Go and TS twins) is the
reference.

```sh
go test ./test/conformance/... -count=1                         # corpus + parity
CONFORMANCE_UPDATE=1 go test ./test/conformance/ \
  -run 'TestCorpus$|TestParity'                                 # rewrite fixtures
CONFORMANCE_FAM=all CONFORMANCE_RUNS=5000 CONFORMANCE_OUT=/tmp/all.json \
  go test ./test/conformance/ -run TestLargeRun -v               # large run
```

Unlike the other suites under `test/`, this package carries no build tag,
so `go test ./...` — `make verify` and CI's `test` lane — runs it. That is
deliberate: the tag-gated suites are gated because they need a service
(`integration`, `complex`) or run for hours (`bench`, `fuzz`), and this one
needs neither, finishing the corpus and all 1850 parity rows in seconds.
`TestLargeRun` is the part that would not fit a commit gate, and an
unset `CONFORMANCE_FAM` skips it.

The one lane that runs it also runs the integration suite, on the same four
cores, so the rows are a CPU budget as much as a check: a v1 family pins 200
seeds and a v2 family 50. What catches a convergence bug is the corpus — a
seed that caught one is minimized into `corpus/` and replayed on every run —
so the seed count only has to be wide enough to catch a runner that drifts
from the other SDK.

## Script

```json
{
  "v": 2,
  "id": "js1436-min-a",
  "issues": ["yorkie-js-sdk#1436", "yorkie#2167"],
  "status": { "go": "known-diverge", "js": "known-diverge" },
  "note": "free text",
  "replicas": 2,
  "delivery": "log",
  "init": "abcd",
  "steps": [ ... ]
}
```

- `v`: `1` or `2`.
- `init`, either form:
  - a string, the text of the single paragraph the document starts with:
    `<doc><p>{init}</p></doc>`, i.e. the tree
    `{type:'doc', children:[{type:'p', children:[{type:'text', value: init}]}]}`;
  - (v2) a tree node object, the whole tree: `{"type":"doc","children":[…]}`.
    A node is a text node `{"type":"text","value":…}` (exactly these two
    fields, `value` a string) or an element `{"type":…}` with a non-empty
    string `type` other than `text`, optional `children` (an array of
    nodes; absent means none, for the root too) and optional `attributes`
    (an object of string→string), and no other field. The root must be an
    element.

  Replica 0 creates the tree with `root.t = new Tree(node)` (Go:
  `SetNewTree("t", node)`) and every other replica applies that change
  before step 0. The init change is not in the server log. Its push ack and
  its delivery carry the initial (empty) version vector in both GC modes.
  Then every replica clears its undo and redo stacks (Go `ClearHistory()`,
  JS `clearHistory()`): the init change is not an edit of the script, and an
  `undo` that reached it would remove the tree.
- `delivery` (v2, optional): `log` (the default) or `exchange`; see Delivery
  and GC.
- Actor of replica `i` is the 24-digit decimal `i+1` zero-padded
  (`000000000000000000000001`), as in fuzz2. Document key `conformance`.
- `status` per SDK, the ledger: `pass`, `known-diverge`, `known-error` or
  `known-fail`, expecting the result `pass`, `diverged`, `error` or `fail`
  respectively. Only corpus scripts carry it.
- Loading is strict and fails the script, naming its file, on: input other
  than whitespace after the script; an unknown field (top level or in a
  step); a `v` other than 1 or 2; a `replicas` that is not a positive
  integer; a `status` key other than `go` and `js` or an unknown `status`
  value; a step without `r` or `op`, or with an `r` that is not an integer in
  `0..replicas-1`; an unknown `op`; a step missing a field its op requires,
  or carrying a field its op does not take (even one valid for another
  op); a `null` field; or a field of the wrong type (see Steps).
- v2 adds these rejections: in a v1 script, a tree node `init`, a
  `delivery` field, a `contents` field or an `update`, `exchange` or
  `expect` step; a malformed tree node (in `init` or
  `contents`, see above); a `delivery` other than `log` and `exchange`; an
  `init` that is neither a string nor an object; a `gen` step of a
  paragraph kind (`ins`, `split`, `del`, `merge`, `style`) when `init` is a
  tree node, and one of a span kind (`sins`, `ssplit`, `senter`,
  `senter11`, `sdel`) when `init` is a string; `pull` or `sync` with `exchange` delivery and `exchange` with
  `log` delivery; an `r` on `exchange` or `expect`; and the per-op rules
  below.

## Steps

Each step has `r` (replica index, an integer) and `op`, and exactly the
fields of its op below: required unless marked optional, none other.
`exchange` and `expect` take no `r`. `from`, `to` and `split` are integers.

| op | fields | effect |
|---|---|---|
| `edit` | `from`, `to`, optional `text` (string), optional `contents` (v2), optional `split` (int) | `t.edit(from, to, text ? {type:'text', value:text} : undefined, split ?? 0)`; with `contents`, `t.editBulk(from, to, contents, split ?? 0)` (Go `EditBulk`) |
| `update` (v2) | `ops` (non-empty array of nested ops) | one `doc.update` running each nested op on `t` in order: one change, one undo unit |
| `style` | `from`, `to`, `attrs` (non-empty object of string→string) | `t.style(from, to, attrs)` |
| `removeStyle` | `from`, `to`, `keys` (non-empty array of strings) | `t.removeStyle(from, to, keys)` |
| `undo` / `redo` | – | `doc.history.undo()` / `redo()`; when the stack is empty it is a noop |
| `pull` | – | `log` delivery only: deliver the next server-log entry this replica has not seen (see Delivery). Noop if none |
| `sync` | – | `log` delivery only: `pull` until none is left |
| `exchange` (v2) | no `r`; optional `orders` | `exchange` delivery only: hand every replica's local changes to the others (see Delivery) |
| `snapshot` | – | replace replica `r` by a document rebuilt from its own snapshot bytes (see Snapshot) |
| `expect` (v2) | no `r`; `xml` (array of strings, one per replica), optional `label` (string) | off run only: compare each replica's `toXML()` with `xml[i]` (see Checks). Changes nothing |
| `gen` | `k` (`ins`, `split`, `del`, `merge`, `style`, or (v2) a span kind `sins`, `ssplit`, `senter`, `senter11`, `sdel`), `f`, `g`, `h` (numbers in [0, 1)), `v` (string) | abstract generator event, resolved on replica `r` exactly as fuzz2 resolves `ins`, `split`, `del`, `merge`, `style`, and the span kinds as below |

An empty `text` counts as absent.

- `contents` (v2): a non-empty array of tree nodes (the `init` node rules,
  any root type), inserted in order. `text` and `contents` are exclusive.
- `update` (v2): each nested op is an `edit`, `style` or `removeStyle` object
  without `r`, held to that op's rules above (fields, types, `contents`).
  Any other op, an `r`, a `null` entry or an empty `ops` is rejected.
- `orders` (v2): an array with one entry per replica; entry `i` lists, in
  arrival order, whose changes replica `i` receives, and must be a
  permutation of the other replicas (no `i`, no repeats, every other replica
  once). Absent: each replica receives the others in ascending index.
- Level-2 splits need nothing new: `split: 2` splits the text's parent and
  its parent in one edit (`<p><span>ab</span></p>` split at 3 with level 2
  gives `<p><span>a</span></p><p><span>b</span></p>`).

Every step that is a local operation (`edit`, `style`, `removeStyle`,
`update`, `undo`, `redo`, `gen`) with `log` delivery appends exactly one
entry to the server log at that step, authored by `r`, carrying the changes
it produced. If it produced no change (generator noop, empty undo stack),
the entry carries no changes but is still appended and still counts for
delivery — this keeps fuzz2's numbering. With `exchange` delivery a local
operation is not pushed: its changes stay among the replica's local changes
until the next `exchange`. It produced a change when the number of local
changes in `doc.createChangePack()` grew.

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

### Span kinds (v2)

The span kinds resolve on the text slots of spans, for trees of the shape
`<doc><p><span>…</span>…</p>…</doc>`. Walk the live children of the root
as paragraphs with a running index `idx` from 0: `idx += 1` for the
paragraph's start; for each live child of the paragraph, a text node adds
its length, and an element (a span) adds 1 for its start, then offers the
slots `k = 0..len(text)` at index `idx + k`, where `text` is the
concatenation of the span's text children, then adds `len(text) + 1`;
`idx += 1` for the paragraph's end. Pick `slots[floor(f * len(slots))]`
(index `i`, offset `k` in a span of length `len`). No slot → noop.

- `sins` → `edit(i, i, text=v)`.
- `ssplit` → `edit(i, i, split=1)`: split the span.
- `senter` → `edit(i, i, split=2)`: Enter as one edit, splitting the span
  and its paragraph.
- `senter11` → one `update` running `edit(i, i, split=1)` then
  `edit(i+1, i+1, split=1)`: Enter as two edits, the span and then the
  paragraph between the two halves. Its step line is
  `d{r+1}.update[split(i);split(i+1)]`.
- `sdel`: noop if `k = len`; otherwise
  `edit(i, i + 1 + floor(g * min(2, len - k)))`.

## Delivery and GC

`log` delivery (the default, and all of v1) is the server log below.
`exchange` delivery (v2) has no server log; see Exchange.

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
- Lagging minVV (large runs only, not part of any record or fixture): a
  runner may offer the `minvv` mode with the race of yorkie#2110, where a
  reply's minVV is taken after another client's push landed but the reply
  carries changes read before it. With `log` delivery, a pack applied to
  replica `r` then also carries `minVV` when, after that pack, the only
  log entry `r` has not seen and did not author is the newest entry of the
  log. Everything else, off mode and the checks included, is unchanged.
  Under this mode GC collects tombstones a pending change still refers to,
  so `error` results ("node not found") are expected; the mode measures
  how much of the suite depends on the #2110 contract. Go:
  `Options.LaggingMinVV`, `CONFORMANCE_GC=lagging`.

### Exchange

`exchange` delivery replays the `exchangeInOrder` helper of yorkie's
`pkg/document/tree_undo_test.go`. An `exchange` step, with `orders`
resolved (absent: ascending index):

1. Collect: for each replica `i`, its local changes as one pack,
   `doc.createChangePack()` sent through the converter. A pack without
   changes is empty.
2. Deliver: for `i = 0..n-1`, for each `j` in `orders[i]`, if `j`'s pack is
   not empty, apply its changes to replica `i` in a pack with
   `Checkpoint(0, 0)` and the initial version vector, through the converter.
3. Ack: for `i = 0..n-1` whose pack is not empty, apply an empty pack with
   `Checkpoint(0, lastClientSeq)` and the initial version vector, where
   `lastClientSeq` is that of the pack's last change.
4. `minvv` mode only: every replica has now received every change, so set
   every row to its replica's version vector, then apply one empty pack with
   `Checkpoint(0, 0)` and `minVV` to every replica, in index order.

A runner must pin step 2's order with a test of its own: a correct CRDT
converges whatever the arrival order, so no script's result, XML or digest
can tell a runner that honours `orders` from one that delivers in ascending
index and happens to parse the field. The runner records, outside the
record, each exchange's deliveries as `d{to}<-d{from}` in the order it
applied them, and the replicas whose pack was not empty. In off and minvv
mode, the tests compare those deliveries, exactly, with the ones `orders`
calls for, restricted to those replicas: a fixed script, and every
exchange of the corpus (for a `known-error` script, the exchanges that
completed before the error), with at least one delivery overall (Go:
`TestExchangeOrders` and `TestExchangeOrdersCorpus`; JS: the
`Tree conformance: exchange orders` tests).

End of run: one `exchange` with default orders, not recorded as a step line.
There is no other end-of-run drain or GC pack. I4 compares off and minvv as
with `log` delivery.

Not modelled (follow-up): a minvv collection while some replica still holds
an unexchanged local edit. Rows only move at an `exchange`, when nothing is
in flight, so GC here never races a concurrent edit; the `log` mode covers
that race.

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
With `exchange` delivery, a `snapshot` of a replica that holds local changes
not yet exchanged throws (the rebuild would drop them), ending the run with
`error`.

## Checks

Run the script in `off` mode, then again in `minvv` mode. If `off` mode
throws, `minvv` mode is not run.

- **I1** (off mode): all replicas have equal `toXML()` and equal shape.
- **I2**: no step threw (either mode).
- **I3** (off mode, at every `snapshot` step and, after the end-of-run
  drain, for every replica): the rebuild gives the same `toXML()` and shape.
- **I4**: final `toXML()` of replica 0 in `minvv` mode equals off mode, and
  all replicas agree in `minvv` mode.
- **E** (v2, off mode): every `expect` step of the script ran and, at each,
  every replica's `toXML()` equalled its `xml` entry. A script without
  `expect` steps holds E. `expect` steps do nothing in `minvv` mode.
- **I6**: not implemented.

A check that could not be evaluated because a run threw is `false`, and a
check that throws counts as `false`: a rebuild that throws in the I3 check
fails I3 (result `fail`) instead of aborting the runner.
`diverged` = I1 fails. A run's result is `pass` only if I1–I4 and E all
hold.

## Canonical strings

- Shape (off mode, includes tombstones), walking `allChildren` / the Go
  equivalent (`Children(true)`) in stored order:
  - id: `{lamport}:{actorHex}:{offset}` from the node's createdAt and offset,
    built from fields, not from an SDK `toString`.
  - text: `{id}{x?}"{value}"` (value unescaped); element:
    `{type}#{id}{x?}[{child},{child}…]`; `x` when removed.
- Step description (one line per step, in step order, from the `off` run):
  - local op that produced a change: `d{r+1}.edit({from},{to},'{text}')`,
    `d{r+1}.split({from})` for `split: 1` without text and `from == to`,
    `d{r+1}.split({from},{split})` for `split` ≥ 2 without text and
    `from == to`, `d{r+1}.edit({from},{to})`,
    `d{r+1}.edit({from},{to},{xml},{xml}…)` with `contents` (one canonical
    XML per node, `,`-separated, no quotes; `split` not shown, as with
    `text`), `d{r+1}.style({from},{to},{k}={v},…)` keys
    sorted, `d{r+1}.removeStyle({from},{to},{k},…)` keys in script order,
    `d{r+1}.undo`, `d{r+1}.redo`,
    `d{r+1}.update[{op};{op}…]` where each `{op}` is the nested op's form
    above without `d{r+1}.` (e.g. `d1.update[edit(1,1,'X');style(7,13,b=1)]`).
    A `gen` step is described by what it
    resolved to: `ins` as `edit(i,i,'v')`, `split` as `split(i)`, `del` and
    `merge` as `edit(from,to)`, `style` as `style(from,to,k=v)`.
  - local op with no change: `d{r+1}.noop`.
  - canonical XML of a script node: a text node is its `value`; an element
    is `<{type}` + ` {k}="{v}"` per attribute, keys sorted + `>` + its
    children + `</{type}>`. Nothing is escaped. It is built from the script
    node, not by the SDK.
  - pull: `pull d{r+1} <- #{logIndex}` or `pull d{r+1} -` when nothing was
    left. `sync` expands to one `pull d{r+1} <- #{logIndex}` line per
    delivered entry and adds nothing when none was left. `snapshot`:
    `d{r+1}.snapshot`.
  - `exchange`: `exchange d1<-[{from},…] d2<-[…] …`, one `d{i+1}<-[…]` per
    replica separated by a space, each listing the resolved order as
    `d{j+1}` separated by `,` (`exchange d1<-[d3,d2] d2<-[d1,d3] d3<-[d2,d1]`).
  - `expect`: `expect` (the label is not part of the line).
  - A step that throws adds no line.

## Record

```json
{
  "id": "…", "n": 2,
  "result": "pass | diverged | error | fail",
  "checks": "11111",
  "steps": ["d1.edit(2,2,'c')", "pull d2 <- #0", "…"],
  "xml": ["<doc>…</doc>", "<doc>…</doc>"],
  "digest": "first 16 hex of sha256(steps.join('\n') + '\n' + xml.join('\n') + '\n' + shapes.join('\n'))",
  "xmlDigest": "first 16 hex of sha256(steps.join('\n') + '\n' + xml.join('\n'))"
}
```

`xml` and shapes are those of the `off` run after the end-of-run drain.
`checks` is five characters, `1` when the check holds and `0` otherwise,
for I1, I2, I3, I4 and E in that order (`"01101"`: diverged, no throw, I3
held, I4 failed, every expect held).

`result`: `error` if I2 failed, else `diverged` if I1 failed, else `fail` if
I3, I4 or E failed, else `pass`. On `error`, `xml` is empty and both digests
are the first 16 hex of `sha256(steps.join('\n') + '\nERROR')` (error
messages differ between SDKs).

## Generator

mulberry32 exactly as fuzz2 (`gen(seed, fam, n, steps)`), with the same
families, weights, pull probability (0.5 when the replica has a pending
entry), value pool and pending count. Output is a script whose steps are
`gen` and `pull` (and `undo`, `redo` and `sync` in the families below that
have them), with `init` `abcd`.

Each step draws, in order: the replica `r = floor(rnd() * n)`; in an
observer family, if `r` is the observer (replica `n-1`), nothing more:
a `sync` step if `r` has a pending entry, no step otherwise. Else one
number for the pull test (`rnd() < 0.5` and a pending entry → `pull`, and
nothing more), then the kind by weight, then `f`, `g`, `h` and the value
`valuePool[c % len]` (`c` counts local ops). Kind `undo` or `redo` emits
an `undo` or `redo` step, after drawing `f`, `g`, `h` and the value like
any local op; every other kind emits a `gen` step. Every local op step
(`gen`, `undo`, `redo`) counts as an entry for the pending count, as the
server log does. A `sync` sets the observer's cursor past every entry.

Weights, in order (the last kind takes what rounding leaves):

| fam | weights |
|---|---|
| is | ins .5, split .5 |
| del | ins .35, split .35, del .3 |
| merge | ins .35, split .35, merge .3 |
| style | ins .35, split .35, style .3 |
| all | ins .3, split .3, del .15, merge .15, style .1 |
| ud | ins .35, del .3, undo .25, redo .1 |
| uds | ins .3, split .2, del .2, undo .2, redo .1 |
| udm | ins .3, split .15, del .1, merge .15, undo .2, redo .1 |
| udst | ins .3, split .15, style .25, undo .2, redo .1 |
| uall | ins .25, split .2, del .1, merge .1, style .05, undo .2, redo .1 |
| span | sins .4, ssplit .15, senter .15, senter11 .3 |
| spand | sins .4, ssplit .15, senter .15, senter11 .15, sdel .15 |

A `span` or `spand` script is v2 and its `init` is the tree
`<doc><p><span>abcd</span></p></doc>`. The script id is
`gen-{fam}-n{n}-s{steps}-{seed}`, with `n{n}o` in an observer family.

| Fixture family | fam | n | steps | observer |
|---|---|---|---|---|
| `is` | is | 2 | 24 | |
| `del` | del | 2 | 24 | |
| `merge` | merge | 2 | 24 | |
| `style` | style | 2 | 24 | |
| `all` | all | 2 | 24 | |
| `all3` | all | 3 | 30 | |
| `ud` | ud | 2 | 24 | |
| `ud3` | ud | 3 | 30 | |
| `uds` | uds | 2 | 24 | |
| `uds3` | uds | 3 | 30 | |
| `udm` | udm | 2 | 24 | |
| `udst` | udst | 2 | 24 | |
| `uall` | uall | 2 | 24 | |
| `uall3` | uall | 3 | 30 | |
| `span` | span | 2 | 24 | |
| `spand` | spand | 2 | 24 | |
| `spand3` | spand | 3 | 30 | |
| `del4o` | del | 4 | 40 | yes |
| `all4o` | all | 4 | 40 | yes |

The undo families cover undo and redo of every edit kind, the span families
splits inside elements (Enter as one edit and as two), and the observer
families a fourth replica that only syncs, so a replica that never edits
still moves the minVV and collects (yorkie#2099).

## Files

`tree/`:

- `corpus/*.json`: scripts with `status`. The runners are strict: the
  result must be exactly the one the status expects. When a change moves a
  script's result (a fix makes it pass, or it starts failing differently),
  update its `status` in the same change. The status pins `result`
  only; `corpus-digests.json` pins the rest of the record.
- `corpus-digests.json`: `{"{script}.json": {result, checks, digest,
  xmlDigest}}` for every corpus script, keyed by file name in sorted order,
  generated by the Go runner. The parity fixtures cannot stand in for it:
  no generated script carries `undo`, `redo`, `snapshot`, `removeStyle`,
  `update` or `contents`, so only this file pins the step lines, xml and
  shapes of those ops byte for byte.
- `parity/{family}.json`: `{fam, n, steps, observer?, seed0: 1, runs,
  diverged, rows: [{seed, result, checks, digest, xmlDigest}]}` generated
  by the Go runner. `observer` is present, `true`, only for an observer
  family. `runs` is 200 for a v1 family and 50 for a v2 one; a runner reads
  it from the header rather than assuming one count.
  `diverged` counts rows whose `result` is `diverged`.

Both runners check `corpus-digests.json`: it must list exactly the files
in `corpus/`, no more and no fewer, and every entry's `result`, `checks`,
`digest` and `xmlDigest` must equal the runner's record of that script. The
Go runner also requires the file to be byte for byte the one it writes.
The Go runner regenerates it with `CONFORMANCE_UPDATE=1` alongside the
parity fixtures; a change that adds a script or moves a record regenerates
it in the same commit. Its provenance is its place in yorkie's `tree/`:
yorkie-js-sdk takes it from there with the rest of the copy.

Both runners check the parity fixtures: the header (`fam`, `n`, `steps`,
`observer`, `seed0`, `runs`, `diverged`) must equal the regenerated one, `rows` must
hold `runs` rows, and every row's `result`, `checks`, `digest` and
`xmlDigest` must match. The Go runner regenerates them with
`CONFORMANCE_UPDATE=1`; a change that moves a row regenerates the fixture in
the same commit, so the diff shows which seeds moved.

Env for large runs (no fixture comparison): `CONFORMANCE_FAM` (a fixture
family name), `CONFORMANCE_RUNS` (default 5000), `CONFORMANCE_SEED0`
(default 1), `CONFORMANCE_OUT` (writes the rows in the parity-fixture shape),
`CONFORMANCE_GC` (`lagging`: the lagging minVV of Delivery and GC).

`TestSpanOracle` (Go only, not part of the runner contract, skipped unless
`CONFORMANCE_ORACLE_OUT` names an output file) enumerates every two- and
three-op combination of the span kinds `sins`, `ssplit`, `senter` and
`senter11` on `<doc><p><span>abcd</span></p></doc>` in four causal shapes and compares each result with the outcomes a flat
token model allows (every op's tokens stay between the two tokens around
its gap, no op lands inside another, text only inside spans). `sdel` is
not among them: the token model has no deletion. The `oracle-*` corpus
scripts come from it.

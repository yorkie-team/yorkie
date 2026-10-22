# Dedup Counter HLL Vectors

`vectors.json` pins the hashing of the dedup counter's HyperLogLog
(`pkg/document/crdt/hll.go`; `packages/sdk/src/document/crdt/hll.ts` in
yorkie-js-sdk) and the register payloads both SDKs accept. A dedup counter
converges only while Go and JS map the same actor string to the same register
update, so a change to either side's hash, UTF-8 handling or register mapping
has to fail a test rather than diverge a counter silently.

This file is the normative format and the SSOT for both runners. The Go runner
is `test/conformance/hll_test.go` (`TestHLLVectors`, `TestHLLRestore`), which
`go test ./...` runs without a build tag — the whole fixture replays in
milliseconds. yorkie-js-sdk takes a copy of this directory the way it takes
`tree/`, with a `SOURCE` file holding the yorkie commit it came from.

```sh
go test ./test/conformance/ -run 'TestHLL' -count=1   # check
CONFORMANCE_UPDATE=1 go test ./test/conformance/ \
  -run TestHLLVectors                                 # rewrite vectors.json
```

Regenerating rewrites what the implementation produces, so a diff in
`vectors` or in a family digest is the review's question: a deliberate change
of the hash, or a regression. The `restore` cases are hand-written in the Go
runner and only copied out, so regenerating cannot move them.

## Format

```json
{
  "v": 1,
  "hash": "xxhash64",
  "precision": 14,
  "registerCount": 16384,
  "maxRegisterValue": 51,
  "vectors": [ … ],
  "families": [ … ],
  "aggregate": { … },
  "restore": [ … ]
}
```

- `v`: format version, `1`.
- `hash`: the hash function `Add` applies to the UTF-8 bytes of its argument.
- `precision`, `registerCount` (`1 << precision`), `maxRegisterValue`
  (`64 - precision + 1`): the parameters both SDKs hold.

### `vectors`

One entry per hand-picked string, in file order:

| field | meaning |
|---|---|
| `id` | name of the case |
| `value` | the string `Add` receives |
| `utf8` | lower-case hex of `value`'s UTF-8 bytes |
| `hash` | 16 lower-case hex digits, `hash` of those bytes |
| `index` | the register `Add` writes, `hash >> (64 - precision)` |
| `rho` | the value it writes into an empty HLL, in `1..maxRegisterValue` |

A runner checks `utf8` against its own encoding of `value` first: that is what
catches an editor, a transfer or a normalizing JSON reader that changed the
combining-mark, ZWJ or NUL cases, before the digests blame the hash for it.
`index` and `rho` are read back from a fresh HLL after one `Add` — one register
must have changed — rather than recomputed from the formula, so the fixture
checks `Add` instead of restating it.

The cases are the ones the cross-SDK audit of yorkie#2274 compared: the empty
string, one ASCII byte, a 24-digit actor id, 31/32/33 bytes (around xxhash64's
32-byte block) and 100 bytes, Hangul, astral emoji, a ZWJ sequence, a combining
mark against its precomposed form (which must hash differently — neither SDK
normalizes), and an embedded NUL.

### `families`

A generated run of strings, pinned by what they fill rather than one entry
each. The `i`-th string of a family is `prefix` followed by `i` in decimal, for
`i` in `[0, count)`, added in that order (order does not matter: the register
array is a max-merge). `utf8` is the hex of `prefix`'s UTF-8 bytes. `digest` is
that family's array alone, on its own HLL, so a divergence names the family.

### `aggregate`

The digest of one HLL holding every `vectors` value, in file order, then every
family's strings, in file order.

### Digests

`aggregate` and a family's `digest` hold:

| field | meaning |
|---|---|
| `count` | `Count()` of that HLL, the estimate |
| `nonZero` | how many of the `registerCount` registers are not 0 |
| `registers` | lower-case hex sha256 of the register array, all `registerCount` bytes, in index order |

`registers` is what pins the hashing; `count` additionally pins the estimator,
which is floating point and must agree between the SDKs.

### `restore`

One register payload per entry, and what `Restore` makes of it:

| field | meaning |
|---|---|
| `id` | name of the case |
| `length` | length of the payload in bytes |
| `registers` | optional, register index in decimal → the value held there; every other register is 0 |
| `accept` | whether `Restore` must accept it |
| `count` | for an accepted payload, the `Count()` it reports after the clamp below (absent means 0) |

Only a length mismatch is rejected, and a rejected payload must leave the HLL
it was handed to unchanged — in Go an error return, in JS a `false` return.

A register above `maxRegisterValue` cannot come from `Add`, and an SDK must not
carry one: an accepted payload ends with every register at
`min(value, maxRegisterValue)`. It is clamped rather than refused because
`Restore` also decodes snapshots and operations already in storage, which a
client may have written before the bound existed; refusing those would make the
document permanently unloadable instead of repairing it. Clamping is
deterministic and idempotent, so replicas restoring the same payload still
converge, and re-serializing writes the repaired registers back.

A runner checks the registers, not only the count: the estimator's small-range
branch reads only which registers are zero, so an unclamped register of 200
reports the same count as the clamped one and the count alone would miss it.

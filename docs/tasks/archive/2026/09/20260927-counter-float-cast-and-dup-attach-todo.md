**Created**: 2026-09-27

# Counter float deltas and duplicate attach (Go/JS parity)

Two gaps from a Go/JS SDK parity audit. JS is the reference behavior.

## G4 — Counter increase with a fractional Double delta

`castToInt`/`castToLong` in `pkg/document/crdt/counter.go` convert a
float with `int32(val)`/`int64(val)`. Go leaves an out-of-range
float-to-int conversion implementation-defined: arm64 saturates, amd64
does not. A JS client sends a `Double` delta as is, so replicas on
different CPUs (and the server's snapshot replay) can disagree.

JS (`packages/sdk/src/document/crdt/counter.ts`):

- Int counter: `bigintToInt32(BigInt(value + removeDecimal(delta)))` —
  add in float64, truncate toward zero, wrap mod 2^32.
- Long counter: `BigInt.asIntN(64, value + BigInt(Math.trunc(delta)))` —
  truncate, wrap mod 2^64.
- NaN and ±Inf: `BigInt()` throws a RangeError, so the increase is
  rejected.

Plan:

- [x] Compute the expected values by running the JS formulas in node.
- [x] Red: table test in `pkg/document/crdt/counter_test.go`.
- [x] Red: `operations.Increase.Execute` on a `Double` delta fails,
      because `negatePrimitive` has no `Double` case (JS negates it).
- [x] Red: `json.Counter.Increase` converts with `int32(float64)` too.
- [x] Fix: deterministic truncate-and-wrap helpers; reject NaN/±Inf with
      a new `ErrNonFiniteNumber`; Int counter adds in float64 like JS.
- [x] Fix: `negatePrimitive` handles `Double`.
- [x] Fix: `json.Counter.Increase` uses the same helpers.
- [x] Green; `make verify`.

## G5 — Duplicate attach of the same key on one client

`Client.Attach` only checks the `Document`'s own status. A second
`*Document` with an attached key reaches the server, whose
`TryAttaching` filter reports it as `ErrClientNotFound` (NotFound).
JS (yorkie-js-sdk#1337) rejects it locally with `ErrAlreadyAttached`,
including a concurrent in-flight attach.

Plan:

- [x] Red: integration test — second `Attach` of the same key returns
      FailedPrecondition, not NotFound; concurrent attach of the same key
      gets one success and one local rejection.
- [x] Fix: `Client.Attach` guard over `attachments` plus an in-flight
      `attaching` set; new `client.ErrAlreadyAttached`.
- [x] Server: both `TryAttaching` backends return
      `ErrDocumentAlreadyAttached` for an attached doc instead of
      `ErrClientNotFound` (mongo disambiguates only on the failure path).
- [x] Re-attach after detach still works (new `Document`, same key).
- [x] Green; `make verify`; `make test`.

## Review

- [x] Self-review rounds logged in the lessons file.
- [x] Rebase on `origin/main`, push, open PR.
- [x] Round 3: fix the `TestWatchLoopInitFailureStopsPump` flake that
      failed CI (on `main` as well).
- [x] Round 3: build the reverse of a `Double` delta on an Integer
      counter from the change it made (CodeRabbit).
- [x] Round 3: `Detach` rejects a resource that does not hold the
      attachment of its key (CodeRabbit).
- [x] Round 3: correct the old-server note in the lessons file.
- [x] Round 3: answer the Deactivate/in-flight attach thread; out of
      scope, see the lessons file.

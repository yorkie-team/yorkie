**Created**: 2026-10-22

# Dedup counter HLL: bound restored registers, pin the hash across SDKs

Issue: yorkie#2274.

## Problem

1. `HLL.Restore` (`pkg/document/crdt/hll.go`) checks only the payload
   length. The JS SDK's `HLL.restore` also rejects any register above
   `maxRegisterValue` (`64 - precision + 1` = 51), the largest value `add`
   can write. A dedup counter carried in a snapshot, or nested in a
   `Set`/`Add` value, with a register of 200 restores in Go and reports a
   count while JS refuses it — the two SDKs disagree on the counter, and a
   client can inflate the server-side count through
   `api/converter/from_bytes.go` → `Counter.RestoreHLL`.
2. Nothing pins the hash across the SDKs. Dedup converges only while Go
   and JS map the same actor string to the same register update. An audit
   found them equal, but no test holds the vectors, so a change to either
   side's hashing or UTF-8 handling would diverge the registers silently.

## Plan

1. `hll.go`: add `hllMaxRegisterValue = 64 - hllPrecision + 1` and reject a
   payload holding a register above it in `Restore`, naming the offending
   index and value. Keep the length check as it is.
2. Add the shared vector fixture `test/conformance/hll/vectors.json` and
   the Go runner `test/conformance/hll_test.go`, in the existing
   conformance package so `go test ./...` runs it. It pins, for a list of
   hand-picked strings (empty, ASCII, an actor id, 31/32/33/100 bytes,
   Hangul, emoji, a ZWJ sequence, a combining mark against its
   precomposed form, a NUL): the UTF-8 bytes, the xxhash64 value, and the
   register index and rho a single `Add` writes. It also pins, for three
   generated families of 20,000 strings in total, the register-array
   sha256, the non-zero register count and `Count()`, and the same three
   over every vector and family together. Restore cases pin which payloads
   both SDKs accept and reject.
3. `test/conformance/hll/README.md`: the normative format of the fixture,
   so the JS runner can replay it from the same file, plus a pointer from
   the suite README.
4. `pkg/document/crdt/hll_test.go`: unit cases for the new rejection and
   for the boundary value 51.

## Non-goals

- Changing the hash function, the precision, or the wire format.
- The JS side: yorkie-js-sdk takes a copy of `test/conformance/hll/` the
  way it already takes `tree/`.

## Acceptance

- `Restore` rejects a payload with a register above 51 and accepts 51.
- The fixture fails the Go suite if the hashing, the UTF-8 handling, the
  register mapping, or `Count()` moves.

# Lessons: HLL register bound and cross-SDK hash vectors

## Notes

- The sentinel bit in `HLL.Add` (`w |= 1 << (hllPrecision - 1)`) caps rho
  at `64 - hllPrecision + 1` = 51, which is exactly the JS
  `maxRegisterValue`. The bound in `Restore` is therefore the inverse of
  what `Add` can produce, not an independent constant.
- Deriving each vector's register index and rho by observing
  `NewHLL()` + `Add` + `Bytes()` keeps the fixture a check on the
  implementation rather than a second copy of the index/rho formula.
- The fixture pins the UTF-8 bytes of every string next to the string
  itself, so an editor or a transfer that normalizes the combining-mark
  and ZWJ vectors fails the test instead of silently changing what is
  being hashed.

- `Restore` is the single decode path for both client input and stored
  data: `api/converter/from_bytes.go` → `fromJSONCounter` serves
  `BytesToSnapshot` and `FromStoredOperations` alike. A new precondition
  there is retroactive — it applies to every byte already in MongoDB — so
  it has to repair rather than refuse. Clamping does both: it is the same
  normalization on the write path, and on the read path it heals a
  payload written before the bound existed. The error return is left for
  the one thing no repair can guess, a payload of the wrong length.
- The bound alone does not stop count inflation. A payload whose 16384
  registers are all 51 is in range yet estimates ~2.7e19, so a forged
  in-range payload still sets the dedup counter arbitrarily; that is
  inherent to HLL, since any register value is reachable from some input
  set. What the bound does buy is a register array the estimator can
  actually produce, and `Count()` saturating at `math.MaxUint64` keeps
  the conversion out of implementation-defined territory. The remaining
  `int32(count)` truncation in `Counter.recomputeValue`
  (`pkg/document/crdt/counter.go:365`) is deterministic but still
  reports a wrapped value; narrowing it belongs with the counter.
- Pinning the clamp needs the registers, not the count: the estimator's
  small-range branch reads only which registers are zero, so a register
  left at 200 reports the same count as one clamped to 51.

## Self review

- Round 1 (correctness, blast radius, security): a rejecting `Restore`
  was found to be a blast-radius and availability regression on three
  out-of-diff consumers — the revision restore and the admin YSON
  path, where `RestoreHLL` becomes `panic`, and the snapshot decode,
  where a document poisoned before the upgrade would never load again —
  and separately that the bound does not stop the inflation it was
  motivated by. Resolved by clamping instead of rejecting and by
  saturating `Count()`; the fixture's `above-max`, `reported-200` and
  `max-byte` cases now pin the clamped registers and count. The `int32`
  truncation in `counter.go` is left to the counter, outside this diff.

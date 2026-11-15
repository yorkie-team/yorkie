# Lessons: Add unit tests for pkg/units

**Created**: 2026-11-15

## What the tests turned up

`FromHumanSize("32.3kB")` returns **32299**, not 32300. `parseSize` multiplies
in `float64` and then truncates with `int64(size)`: `32.3 * 1000` is
`32299.999999999996`, so the truncation drops a whole byte. The same shape
applies to any fractional input whose product is not exactly representable; the
binary path happens to be safe for `32.3KiB` (`33075.2`) only because that
product rounds above the integer.

Per the issue, the implementation is left alone — `size.go` is a verbatim copy
of `docker/go-units`, and upstream has the same behaviour. The test pins 32299
with a comment, and the PR body describes it so a maintainer can decide whether
to diverge (`int64(math.Round(size))` would fix it, at the cost of changing
truncation semantics for every other input).

Two more behaviours look surprising enough to be worth pinning rather than
leaving implicit:

- The suffix table, not the spelling, picks the multiplier. `FromHumanSize
  ("32KiB")` is 32000 and `RAMInBytes("32MB")` is 33554432; the "i" is only
  validated, never interpreted.
- `HumanSize` rounds after the unit is chosen, so 999999 formats as `"1000kB"`
  rather than `"1MB"`.

## Process notes

- `CustomSize` takes the abbreviation slice as a parameter but the package's own
  tables are unexported. Keeping the test in the external `units_test` package
  (the repo convention) meant restating the two tables in the test file rather
  than switching to an internal test package for one function.
- Walking `go tool cover` output rather than the summary percentage found the
  one branch the first draft missed — a unit letter followed by a non-`b`
  character, e.g. `"32ki"`. Tests now cover 100% of the package's statements.

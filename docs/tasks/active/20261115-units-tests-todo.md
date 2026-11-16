# Add unit tests for pkg/units

**Created**: 2026-11-15

## Why

`pkg/units` is vendored from `docker/go-units` (`size.go`, `duration.go`) and
ships with no tests at all — `go test ./...` reports `[no test files]` for it.
Its parsing and formatting helpers back the size limits and durations exposed in
configuration, so a silent regression in them — an off-by-one at a unit
boundary, a suffix that stops erroring — would surface as a misconfigured server
rather than a failing build.

The package is small and its exported surface is pure functions, so table-driven
tests pin every branch cheaply: the decimal/binary split, the exact 1000 and
1024 boundaries with the value one below each, and the inputs that must return
an error.

## Plan

- [x] `pkg/units/size_test.go`: `FromHumanSize`, `RAMInBytes`, `HumanSize`,
      `HumanSizeWithPrecision`, `BytesSize`, `CustomSize`
- [x] `pkg/units/duration_test.go`: `HumanDuration` across every branch and its
      boundaries
- [x] Only test files change; any behaviour that looks wrong is recorded in the
      lessons file and the PR body, not fixed here
- [x] `make lint` and `go test ./pkg/units/...` green (100% of statements)

## Out of scope

- Changing `size.go` or `duration.go`. They are a verbatim copy of upstream;
  diverging from it is a separate decision with its own review.

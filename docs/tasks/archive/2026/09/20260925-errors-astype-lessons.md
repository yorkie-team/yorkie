**Created**: 2026-09-25

# Lessons — Adopt `errors.AsType`

## Notes

- `errors.AsType[E error](err error) (E, bool)` is a drop-in for the
  `var t T; errors.As(err, &t)` pair. The type parameter is the error
  type itself, not a pointer to it: a target that was `*connect.Error`
  becomes `AsType[*connect.Error](err)`.
- Two call sites in `pkg/errors` (`StatusOf`, `metadata.go`) precede the
  `errors.As` with a direct type assertion on the unwrapped error. That
  assertion is redundant — `As`/`AsType` already tests `err` itself
  before unwrapping — but removing it is a behaviour-neutral cleanup
  outside this issue's checklist item, so it was left alone.
- `connectCodeOf` in `server/rpc/connecthelper/errors.go` used the
  `if connectErr := new(connect.Error); errors.As(...)` form, which
  allocates a throwaway `connect.Error` on every call just to give `As` a
  non-nil target. `AsType` drops the allocation as a side effect.

- `go fix ./...` under Go 1.26 is not quiet on this tree: 45 files, 148
  lines. The breakdown matters more than the count — 76 lines are
  `interface{}` → `any`, ~70 are `for i := 0; i < n; i++` →
  `for i := range n`, and only three are substantive (`fmt.Appendf` in
  `pkg/cmap`, `slices.Contains`, `reflect.TypeFor`). It also rewrites
  `api/yorkie/v1/capabilities.go`, which is off-limits. This is exactly
  the "cosmetic-only bulk rewrites" #1872 says to exclude, so the run
  was reverted wholesale. Anyone revisiting the `go fix` checklist item
  should cherry-pick those three and leave the rest.

## Review rounds

- `/self-review` was not run: this autonomous run is granted no tool that
  can dispatch the reviewer subagent. Recording that here rather than
  letting a skipped round read as a clean one. Review is left to CI,
  `@claude review`, and a human.

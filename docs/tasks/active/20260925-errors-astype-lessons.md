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

## Review rounds

- `/self-review` was not run: this autonomous run is granted no tool that
  can dispatch the reviewer subagent. Recording that here rather than
  letting a skipped round read as a clean one. Review is left to CI,
  `@claude review`, and a human.

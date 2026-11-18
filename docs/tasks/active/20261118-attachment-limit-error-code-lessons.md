# Lessons: Give ErrTooManyAttachments an error code

## Notes

- `WithCode` returns a *new* `errorWithStatus` value rather than mutating the
  receiver, so the code has to be attached where the package-level variable is
  declared — attaching it at a call site would be dropped.
- `ErrorInfoOf` reaches the code through `errors.AsType[StatusError]`, which
  walks the `Unwrap` chain, so the `fmt.Errorf("%d attachments allowed per
  document: %w", ...)` wrapper in `IsAttachmentLimitExceeded` does not hide it.
- `ErrTooManySubscribers` already had `.WithCode("ErrTooManySubscribers")`; the
  issue's "if it lacks one" clause did not apply.

## Self review

Not run: this change was produced in the autonomous one-shot harness, which is
granted no tool that can dispatch the reviewer subagent. The PR's reviewers are
CI, `@claude review`, and a human.

# Give ErrTooManyAttachments an error code

**Created**: 2026-11-18

## Problem

`ErrTooManyAttachments` (`api/types/project.go:31`) is built as
`errors.ResourceExhausted("attachment limit exceeded")` with no
`.WithCode(...)`. The `code` entry in the Connect error metadata therefore
reaches clients as the empty string, so the JS SDK's
`Code.ErrTooManyAttachments` check can never match.

Clients that need to tell "the document is full" from other failures have to
match the Connect status `ResourceExhausted` together with the message
string, because `ErrDocumentSizeExceedsLimit` and `ErrChangeTooLarge` carry
the same status.

`ErrTooManySubscribers` (`server/backend/pubsub/pubsub.go:36`) already carries
`.WithCode("ErrTooManySubscribers")`, so it needs no change.

## Goal

`ErrTooManyAttachments` carries the code `ErrTooManyAttachments`, and that
code survives the `fmt.Errorf("%w")` wrapping in
`Project.IsAttachmentLimitExceeded` out to the RPC metadata.

## Plan

- [x] `.WithCode("ErrTooManyAttachments")` on the error
- [x] Unit test in `api/types/project_test.go`: the error returned by
      `IsAttachmentLimitExceeded` reports the code through
      `errors.ErrorInfoOf`, and still satisfies
      `errors.Is(err, ErrTooManyAttachments)`
- [x] `make lint`, targeted `go test ./api/types/...`

## Out of scope

Other uncoded errors in the tree. The issue names only these two.

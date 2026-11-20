**Created**: 2026-11-20

# Sign auth webhook requests with X-Signature-256

Issue: yorkie-team/yorkie#2416

## Problem

`server/rpc/auth/webhook.go` calls `AuthWebhookClient.Send` with an empty
HMAC key, so auth webhook requests carry no `X-Signature-256` header.
`pkg/webhook/client.go` only sets the header when the key is non-empty.
Event webhooks, by contrast, sign with the project's secret key
(`server/backend/webhook/manager.go`, via `WebhookAttribute.SigningKey`).

A receiver that guards both endpoints with one HMAC verifier therefore has
to refuse the auth webhook call: the event webhook answers 200 while the
auth webhook answers 401. Without a signature, an auth webhook endpoint on
a public ingress can only trust the token in the body, and anyone who can
reach it can use it as a "would this token be allowed" oracle.

## Approach

Pass `prj.SecretKey` where `""` is passed today. The project reaching
`verifyAccess` comes from `projects.From(ctx)` (loaded by API key) or from
the caller of `RecheckAccess`, neither of which goes through
`Project.WithoutSecretKey()` — that redaction is only on the admin read
path — so the key is populated.

Nothing else changes: the signed bytes are the same body that is already
sent, the auth webhook cache key is `publicKey + ":auth:" + body` and is
untouched, and a receiver that ignores the header sees no difference.

Not an architecture change, so no `docs/design/` entry; the behavior is one
header on an existing request.

## Steps

- [x] `server/rpc/auth/webhook.go`: pass `prj.SecretKey` as the HMAC key.
- [x] Unit test in `server/rpc/auth`: the webhook receives a
      `X-Signature-256` that verifies against the project's secret key over
      the exact request body.
- [x] Integration test in `test/integration/auth_webhook_test.go`: the auth
      server asserts a valid signature on every auth webhook call, mirroring
      `test/integration/event_webhook_test.go`.
- [x] `make lint` and `go test ./server/rpc/auth/... ./pkg/webhook/...`.

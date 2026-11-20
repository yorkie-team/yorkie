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

## Known limitation

The project secret key is also a bearer credential: `API-Key <secretKey>`
authenticates project-scoped `AdminService` calls with `database.Admin`
authority (`server/rpc/interceptors/admin.go`). So a receiver that wants to
verify the signature has to hold a credential that can also drive the
project's admin API. That property is not introduced here — event webhooks
already sign with the same key (`server/packs/pushpull.go`,
`server/backend/webhook/manager.go`) — and verification stays opt-in: a
receiver that ignores `X-Signature-256` is unaffected, and the HMAC tag
does not disclose the key.

Narrowing it means a webhook-only signing secret on the project, separate
from the secret key: a new `ProjectInfo` field, `api/types.Project`, the
`AdminService` update path and its generated protobuf, plus rotation and
migration for existing projects. That is its own change, tracked apart
from this one.

Deriving a per-family key here instead — `HMAC(secretKey, "auth-webhook")`
— would stay inside this file and would give both key separation and
domain separation against the event webhook body. It is not taken because
it does not solve the reported problem: #2416 asks for the signature the
receiver already verifies ("the same key the event webhook signs with, so
one verifier works for both"), and the endpoint that prompted the issue
verifies with the project's secret key today. Signed with a derived key,
that endpoint still answers `401` and stays blocked, now needing a second
key that nothing in the API hands out. Key separation has to arrive for
both webhook families at once, through the stored-secret change above.

## Steps

- [x] `server/rpc/auth/webhook.go`: pass `prj.SecretKey` as the HMAC key.
- [x] Unit test in `server/rpc/auth`: the webhook receives a
      `X-Signature-256` that verifies against the project's secret key over
      the exact request body.
- [x] Integration test in `test/integration/auth_webhook_test.go`: the auth
      server asserts a valid signature on every auth webhook call, mirroring
      `test/integration/event_webhook_test.go`.
- [x] `make lint` and `go test ./server/rpc/auth/... ./pkg/webhook/...`.

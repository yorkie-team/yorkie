# Lessons — Sign auth webhook requests with X-Signature-256

## Notes

- The signing plumbing already existed end to end: `webhook.Client.Send`
  takes an `hmacKey` and `createSignature` returns an empty string for an
  empty key, which `buildRequest` then omits. The auth path was the only
  caller passing `""`, so the fix is the argument, not the mechanism.
- `Project.WithoutSecretKey()` only runs in `server/projects`
  (`redactForRole`, for a caller whose role cannot read the secret key).
  The RPC path builds the project from `ProjectInfo.ToProject()`, which
  copies `SecretKey`, so `verifyAccess` can sign with it.

## Review rounds

- Self-review was not run: this run is headless and granted no tool that
  can launch the reviewer subagent. The PR is opened as a draft for CI,
  `@claude review` and a human reviewer.
- Round 1 (security lens) raised one blocking item: an auth webhook
  receiver that verifies the new signature has to hold the project secret
  key, which `server/rpc/interceptors/admin.go:347-366` also accepts as an
  `API-Key` bearer credential with `database.Admin` authority
  (`admin.go:101`). Left unchanged, for two reasons recorded here with
  their evidence:
  - It is not introduced here. The event webhook already signs with the
    same value: `server/packs/pushpull.go:248` hands `project.SecretKey`
    to `types.NewEventWebhookInfo` as the signing key, and
    `server/backend/webhook/manager.go:101` passes it to
    `webhook.Client.Send`. Any receiver verifying event webhooks already
    holds an admin-capable secret.
  - Narrowing it is a different change. A webhook-only signing secret has
    to cover both webhook families at once, or the two signatures diverge
    and the one-verifier property this task exists for is lost. That
    means `ProjectInfo`, `api/types.Project`, the `AdminService` update
    path and its generated protobuf, plus rotation and migration —
    none of them in this task's diff.

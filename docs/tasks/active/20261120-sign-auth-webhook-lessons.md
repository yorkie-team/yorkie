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

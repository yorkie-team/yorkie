# Lessons: Reject malformed HLL register payloads when YSON is parsed

**Created**: 2026-11-18

## Notes

- The issue (filed from a review of #2278) described two ways to reach the
  panic: a wrong-length payload and a register value above
  `hllMaxRegisterValue`. Only the first is still reachable on `main` —
  `HLL.Restore` now clamps out-of-range register values instead of returning
  an error, so length is the single remaining error condition. The fix is
  therefore narrower than the finding reads.
- `yson.FromCRDT` always produces a 16384-byte payload from a live counter,
  and `api/converter/from_bytes.go` already returns an error rather than
  panicking, so `yson.Unmarshal` is the only ingress for an attacker-chosen
  payload. Validating in `parseDedupCounter` closes it for every caller.

## Self review

- Not run: this branch was produced by the autonomous agent workflow, which
  is granted no tool that can launch a reviewer subagent. The PR opens as a
  draft; CI, `@claude review` and a human reviewer stand in for the rounds.

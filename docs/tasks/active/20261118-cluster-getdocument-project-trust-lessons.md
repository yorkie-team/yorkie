**Created**: 2026-11-18

# Lessons — Stop trusting the wire Project in ClusterService GetDocument

## Notes

- `documents.FindDocInfoByKey` only reads `project.ID` from the project it is
  handed, so `GetDocument` never needed the rest of the wire body. Loading the
  stored project rather than passing a bare ID keeps the handler shaped like
  `DetachDocument` and `RevalidateAccess`, and makes an unknown project ID fail
  as "project not found" instead of "document not found".
- `FindProjectInfoByID` is served from the project cache, so the extra read
  costs nothing in the steady state — the same argument #2330 made for
  `DetachDocument`.
- `*api.Project` getters (`GetId()`) are nil-safe; direct field access
  (`converter.FromProject`) is not. Every ClusterService handler that takes a
  message field must go through a getter or an explicit nil check.

- The all-zero ObjectID is not a free "nothing names this" value here:
  `database.ZeroID` is `DefaultProjectID`, which
  `EnsureDefaultUserAndProject` writes on every server start. A negative test
  that used it as an unknown project resolved the default project instead and
  passed the whole RPC, so the integration test went green on the handler's
  success path while claiming to cover its failure path. Negative-ID fixtures
  need a value the schema reserves for nothing — and an assertion that it
  differs from the project under test, so a future reservation is caught.

## Self review

Not run: this branch was produced by the autonomous agent harness, which is
granted no tool that can dispatch a reviewer subagent. The PR's reviewers are
CI, `@claude review`, and a human.

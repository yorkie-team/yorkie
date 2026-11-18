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

## Self review

Not run: this branch was produced by the autonomous agent harness, which is
granted no tool that can dispatch a reviewer subagent. The PR's reviewers are
CI, `@claude review`, and a human.

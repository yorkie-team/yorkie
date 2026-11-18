# Lessons: the presence fold's size bound is per put, not per pack

**Created**: 2026-11-18

## Notes

- The per-put bound and the per-pack budget are the same measure
  (`presenceSize`) applied at two scopes. Keeping the over-budget path
  identical to the no-base path meant no new error, no new client contract
  and no new SDK behavior: the client already knows how to answer
  `ErrPresenceBaseUnavailable` by resending full puts.
- Only folded puts are charged. A put the client sent itself is already paid
  for in request bytes, so charging it would refuse honest packs without
  removing any amplification.
- `/self-review` was not run: this run is granted no tool that can dispatch a
  reviewer subagent. The PR's reviewers are CI, `@claude review` and a human.

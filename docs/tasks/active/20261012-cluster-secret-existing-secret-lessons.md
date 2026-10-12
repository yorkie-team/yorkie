# Lessons: cluster secret from an existing Secret

**Created**: 2026-10-12

## Notes

- A chart-level fix for a server default is only half the job: #2145 warned
  about the open ClusterService, but production stayed open because the chart
  offered no way to set the secret without committing it. Check that the
  deployment path can actually adopt a hardening before closing its issue.
- The cluster secret is checked asymmetrically (a pod with no secret accepts
  everything), so turning it on fails only old-to-new calls, while changing it
  fails both directions. Rollout notes must say which.

## Self Review

- Round 2 (security): offering a safer way to configure a credential does not
  change the default — the chart still rendered with no cluster secret, and an
  operator who never reads the value comment gets the open service either way.
  Where the chart already hard-fails on a missing admin key for the same class
  of exposure, the cluster secret had to become required too. Fixed by failing
  the render unless one of the two values holds a value, and dropping the
  `if` around `YORKIE_CLUSTER_SECRET` now that one always does.
- Round 1 (correctness/docs): no blocking findings. Fixed: the rollout
  paragraph understated a changed secret (calls fail both ways) and said a
  manual restart is always needed (turning it on rolls the Deployment by
  itself); `docs/design/cluster-service-auth.md` still said the secret is
  passed as `--cluster-secret`. Left as known limitation: a blank secret is
  trimmed to empty by the server and leaves the service open, unchanged by
  this PR.

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

- Round 4 (docs/security): "all five examples" was counted from the install
  sections alone — the signing-key section's own two installs and its rotation
  upgrade still omitted the cluster secret. When a value becomes required,
  grep the whole file for the command name rather than fixing the sections the
  change happened to touch. Separately, teaching operators to re-pass the two
  secrets on upgrade made the omission of `mongodb.credentials.*.password`
  worse, not better: those have defaults (`admin`/`useradmin`/`clusteradmin`,
  published in `values.yaml`), so an upgrade without them silently rewrites
  `mongodb-users` — the Percona `secrets.users` Secret — with public values. A
  documented upgrade command has to carry every value whose default is unsafe,
  not just the ones that fail the render when missing.
- Round 3 (docs): making a value required at render time invalidates every
  documented command that omits it, including the ones in the same README.
  The install examples (Minikube dev, with MongoDB, with external MongoDB) and
  the upgrade examples still passed only the signing key, and the upgrade
  section carried a pre-existing-release warning for the signing key but none
  for the cluster secret. Fixed by adding the cluster secret to all five
  examples and a matching `[!IMPORTANT]` callout for `--reuse-values` upgrades.
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

**Created**: 2026-11-19

# Move the analytics chart's init script out of configmap.yaml

Issue: #2307 (follow-up filed off the review panel on #2296).

## Problem

`server/backend/warehouse/provisioning_test.go` guards the StarRocks
provisioning that every shipped path performs — the inline `ROLLUP` clause,
the `ALTER ... ADD ROLLUP`, the `SELECT 1 FROM rl_session_daily [_SYNC_MV_]`
wait, and the fact that the rollup step runs after the routine loads and
never fails the run. It reads two sources:

- `build/docker/analytics/init-starrocks-database.sh` (plus the SQL beside it)
- `build/charts/yorkie-analytics/templates/starrocks/configmap.yaml`

`.github/workflows/ci.yml` decides whether the `build` job — which is what
runs `go test` — needs to run, using `dorny/paths-filter` with
`predicate-quantifier: every` over `'**'` plus a list of negations. One of
those negations is `!build/charts/**/*.yaml`. The chart's whole init script
lives inside that one YAML file, so a pull request that edits only it is
classified documentation and the `build` job is skipped entirely. The guard
is bypassed by exactly the class of change it exists to catch: weakening the
`|| exit 1`, renaming the rollup, or dropping the wait in the chart script
would ship green. The docker `.sh`/`.sql` half is not excluded, so only the
chart half is unguarded.

## Approach

The issue names two fixes: scope the negation in `ci.yml`, or move the chart
script out of a `.yaml` file. This run may not touch `.github/workflows/**`,
so it takes the second — which is the better one anyway: a 400-line bash and
SQL payload embedded in a YAML block scalar gets no shell/SQL tooling, no
syntax highlighting, and no diff that reads as code.

Move the five `data` entries of
`build/charts/yorkie-analytics/templates/starrocks/configmap.yaml` into
`build/charts/yorkie-analytics/files/` as real `.sql` and `.sh` files, and
render the ConfigMap from them with `.Files.Get`. The two files that carry
Helm interpolation (`{{ .Values.namespace }}` in the shell script, the
Kafka broker/topic values in the routine-load SQL) go through `tpl` so the
rendered output is byte-identical to today's.

`.sql` and `.sh` match no negation in `ci.yml`, so a chart-only edit to the
init script now counts as code and the `build` job runs.

## Steps

- [x] Extract the five `data` keys into `build/charts/yorkie-analytics/files/`
      verbatim (dedented by four spaces).
- [x] Rewrite `configmap.yaml` to read them with `.Files.Get`, wrapping the
      two templated ones in `tpl`.
- [x] Verify `helm template` renders byte-identical ConfigMap data to the
      pre-change chart.
- [x] Repoint `provisioning_test.go` at the new paths and pin the invariant:
      a guarded provisioning source may not be a `build/charts/**/*.yaml`
      file, because `ci.yml` classifies those as documentation.
- [x] `make lint` + `go test ./server/backend/warehouse/...`.

## Out of scope

- `.github/workflows/ci.yml` itself (this run cannot edit it).
- `templates/kafka/configmap.yaml`: no test reads it, so nothing is bypassed
  there.

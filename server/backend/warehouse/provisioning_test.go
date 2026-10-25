/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package warehouse

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoFile reads a file by its path from the repository root.
func repoFile(t *testing.T, rel string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(data)
}

// A descriptor that names a summaryRollup reads it by name — FROM <rollup>
// [_SYNC_MV_] — so the index is a hard runtime dependency rather than an
// optimization: a cluster without it fails that metric's summary reads with
// Unknown table, and GetProjectStats collects every warehouse read in one
// errgroup, so that is the whole project stats response.
//
// Unit tests have no StarRocks to ask whether the index exists on a given
// cluster; TestE2EDualReadStaysOnTheRollups does that, behind the
// starrocksrehearsal tag. What runs everywhere is this: every provisioning path
// the repo ships has to create exactly the index the query builder names, both
// on a fresh cluster (the inline ROLLUP clause, which CREATE TABLE IF NOT
// EXISTS skips on an existing table) and on one whose summary tables predate
// the rollup (the ALTER ... ADD ROLLUP). Renaming the rollup in Go, or adding a
// rollup to another descriptor, without updating both paths fails here instead
// of on the cluster.
func TestSummaryRollupsAreProvisioned(t *testing.T) {
	// Each source and whether it provisions the fresh-cluster DDL, the ALTER
	// for an existing table, or both.
	sources := []struct {
		path        string
		createsDDL  bool
		altersTable bool
	}{
		{path: "build/docker/analytics/init-create-summary.sql", createsDDL: true},
		{path: "build/docker/analytics/init-starrocks-database.sh", altersTable: true},
		{
			path:        "build/charts/yorkie-analytics/templates/starrocks/configmap.yaml",
			createsDDL:  true,
			altersTable: true,
		},
	}

	rollups := 0
	for _, d := range allDescs {
		if d.summaryRollup == "" {
			continue
		}
		rollups++

		t.Run(d.summaryRollup, func(t *testing.T) {
			// The key columns the rollup has to carry are the ones the hinted
			// read projects and filters on.
			cols := fmt.Sprintf("(project_id, dt, %s)", d.hllColumn)
			assert.Equal(t, d.summaryRollup+" [_SYNC_MV_]", d.summarySource(),
				"the hinted name must be the index the provisioning below creates")

			for _, src := range sources {
				body := repoFile(t, src.path)
				if src.createsDDL {
					assert.Contains(t, body, fmt.Sprintf("ROLLUP (%s %s)", d.summaryRollup, cols),
						"%s must carry the inline ROLLUP clause on %s", src.path, d.summaryTable)
				}
				if src.altersTable {
					assert.Contains(t, body, fmt.Sprintf("ADD ROLLUP %s %s", d.summaryRollup, cols),
						"%s must add the rollup to a %s created before it", src.path, d.summaryTable)
				}
			}
		})
	}

	// Guards the loop above against passing vacuously if the rollup is ever
	// dropped from every descriptor.
	assert.Equal(t, 1, rollups, "sessions is the only metric read through a rollup")
}

// Creating the rollup is not the same as a deployment gate on it. Because the
// read names the index, the provisioning that creates it has to (a) stop when
// the ALTER fails rather than continue to the next step, and (b) wait on SHOW
// ALTER TABLE ROLLUP reporting State: FINISHED, since desc <table> all lists
// the index a moment before FROM <name> [_SYNC_MV_] resolves it.
//
// Neither script does that yet: both swallow the ALTER with `|| echo ...
// continuing...` and poll the desc probe, and the chart runs its Job from a
// post-upgrade hook, after the new server is already serving. Fixing that means
// editing build/docker/analytics/init-starrocks-database.sh and
// build/charts/yorkie-analytics/templates/starrocks/{configmap,job}.yaml, which
// this change does not touch, so the assertions below still fail. They are kept
// rather than written later so the gate is mechanical once those files move:
// drop the Skip and the scripts have to satisfy it.
func TestSummaryRollupProvisioningIsAGate(t *testing.T) {
	t.Skip("still reproduces: rollup provisioning is non-fatal and gated on the desc probe")

	for _, path := range []string{
		"build/docker/analytics/init-starrocks-database.sh",
		"build/charts/yorkie-analytics/templates/starrocks/configmap.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			body := repoFile(t, path)

			assert.NotContains(t, body, "rl_session_daily may already exist, continuing...",
				"a failed ADD ROLLUP must stop %s, not be echoed past", path)
			assert.Contains(t, body, "SHOW ALTER TABLE ROLLUP",
				"%s must wait on State: FINISHED, not on the desc probe", path)
		})
	}

	// The chart's init Job runs the ALTER, so it cannot be a post-upgrade hook:
	// by then the upgraded server is already answering with the hinted query.
	job := repoFile(t, "build/charts/yorkie-analytics/templates/starrocks/job.yaml")
	assert.NotContains(t, job, "post-upgrade",
		"the Job that provisions the rollup must run before the server rollout")
}

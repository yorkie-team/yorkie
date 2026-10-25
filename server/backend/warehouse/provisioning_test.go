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

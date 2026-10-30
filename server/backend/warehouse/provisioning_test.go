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
	"strings"
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
// [_SYNC_MV_]. Without the index the reads fall back to the summary table's
// channel-day index, which is correct but what misses the dashboard's deadline.
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

// The provisioning waits on the read the server sends rather than on desc
// <table> all, which lists the index while the build is still running, and
// the wait is bounded. Nothing in the rollup step fails the run: the server
// reads the slower index while the rollup is missing, so a failed ALTER or a
// build still running would only fail the analytics release, and the Job would
// rerun the whole script. A set -e script has to guard each command that may
// fail. The step runs after the routine loads, so it cannot hold back
// ingestion either.
func TestSummaryRollupProvisioningNeverFailsTheRun(t *testing.T) {
	for _, d := range allDescs {
		if d.summaryRollup == "" {
			continue
		}

		for _, path := range []string{
			"build/docker/analytics/init-starrocks-database.sh",
			"build/charts/yorkie-analytics/templates/starrocks/configmap.yaml",
		} {
			t.Run(d.summaryRollup+"/"+path, func(t *testing.T) {
				body := repoFile(t, path)

				step := strings.Index(body, "Adding the coarse session rollup")
				loads := strings.Index(body, "init-create-routine-load.sql")
				require.GreaterOrEqual(t, step, 0, "%s must add the rollup", path)
				require.GreaterOrEqual(t, loads, 0, "%s must create the routine loads", path)
				assert.Greater(t, step, loads, "%s must add the rollup after the routine loads", path)

				rollup := body[step:]
				assert.NotContains(t, rollup, "exit 1", "%s must not fail on the rollup", path)
				assert.Regexp(t, `ADD ROLLUP `+d.summaryRollup+`[^\n]*;" \\\n\s*\|\| echo`, rollup,
					"%s must report a failed ADD ROLLUP and go on", path)
				assert.Regexp(t, `SHOW ALTER TABLE ROLLUP FROM yorkie\\G"\)( \\\n\s*)? \|\| jobs=''`, rollup,
					"%s must go on when SHOW ALTER TABLE ROLLUP fails", path)
				assert.Contains(t, rollup, "SELECT 1 FROM "+d.summarySource()+" LIMIT 1",
					"%s must wait until the hinted read resolves", path)
				assert.Regexp(t, d.summaryRollup+` is not queryable yet[^\n]*\n\s*break`, rollup,
					"%s must stop waiting on a running build, not fail the run", path)
			})
		}
	}
}

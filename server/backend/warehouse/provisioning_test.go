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
	"regexp"
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

// Creating the rollup is not enough: because the read names the index, the
// provisioning has to be a gate on it. Both scripts have to stop when the ALTER
// fails rather than echo past it, and wait until the read the server sends
// resolves, failing if it never does. desc <table> all is not that wait: it
// lists the index while the build is still running. The gate runs after the
// routine loads, so a rollup failure does not hold back ingestion.
func TestSummaryRollupProvisioningIsAGate(t *testing.T) {
	// How each script stops on a failed command: the docker one runs under
	// set -e, the chart's has no set -e and exits explicitly.
	scripts := []struct {
		path    string
		failure string
	}{
		{path: "build/docker/analytics/init-starrocks-database.sh", failure: `(?m)^set -e$`},
		{
			path:    "build/charts/yorkie-analytics/templates/starrocks/configmap.yaml",
			failure: `ADD ROLLUP [^\n]*;" \\\n\s*\|\| exit 1`,
		},
	}

	for _, d := range allDescs {
		if d.summaryRollup == "" {
			continue
		}

		for _, sc := range scripts {
			t.Run(d.summaryRollup+"/"+sc.path, func(t *testing.T) {
				body := repoFile(t, sc.path)

				assert.Regexp(t, sc.failure, body, "a failed ADD ROLLUP must stop %s", sc.path)
				// Whatever follows the ALTER on its line or a continuation line
				// may only be the explicit exit, never a fallback that swallows it.
				alter := regexp.MustCompile(`ADD ROLLUP ` + d.summaryRollup + `[^\n]*;"((?: \\\n)?\s*\|\|[^\n]*)?`)
				matches := alter.FindAllStringSubmatch(body, -1)
				require.NotEmpty(t, matches, "%s must add %s", sc.path, d.summaryRollup)
				for _, m := range matches {
					if tail := strings.TrimSpace(strings.TrimLeft(m[1], " \\\n")); tail != "" {
						assert.Equal(t, "|| exit 1", tail, "%s must not fall back past a failed ADD ROLLUP", sc.path)
					}
				}
				assert.Contains(t, body, "SELECT 1 FROM "+d.summarySource()+" LIMIT 1",
					"%s must wait until the hinted read resolves", sc.path)
				assert.Regexp(t, d.summaryRollup+` is not queryable[^\n]*\n\s*exit 1`, body,
					"%s must fail when the hinted read never resolves", sc.path)

				assert.NotContains(t, body, "set +e", "%s must not turn off failing on errors", sc.path)

				gate := strings.Index(body, "until rollup_ready")
				loads := strings.Index(body, "init-create-routine-load.sql")
				require.GreaterOrEqual(t, gate, 0, "%s must wait on rollup_ready", sc.path)
				require.GreaterOrEqual(t, loads, 0, "%s must create the routine loads", sc.path)
				assert.Greater(t, gate, loads, "%s must gate on the rollup after the routine loads", sc.path)
			})
		}
	}
}

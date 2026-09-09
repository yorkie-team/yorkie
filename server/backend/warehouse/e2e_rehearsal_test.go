//go:build starrocksrehearsal

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
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
)

// The rehearsal exercises the real StarRocks read path — dial, build, execute,
// scan — against a live cluster carrying this repo's schema
// (build/docker/analytics/init-create-{table,mv,summary}.sql). It is gated
// behind the `starrocksrehearsal` build tag and a SR_DSN env var, so it never
// runs in CI, which has no StarRocks:
//
//	SR_DSN='root:@tcp(127.0.0.1:9931)/yorkie' \
//	  go test -tags starrocksrehearsal ./server/backend/warehouse/ -run TestE2E -v
//
// It SEEDS ITS OWN DATA: base events and summary rows for rehearsalProject.
// Point SR_DSN at a disposable cluster, never at one holding real events. The
// writes are idempotent — base rows are the same ids every run and the summary
// tables merge sketches — so it can be re-run freely.

// rehearsalProject owns every row the rehearsal writes.
var rehearsalProject = types.ID("000000000000000000e2e001")

// rehearsalDays is how many days back the seeded window reaches.
const rehearsalDays = 6

// seedRehearsal writes base events for [today-rehearsalDays, today] and summary
// rows for all but the last two of those days.
//
// The two-day hole is the point. The last completed day (yesterday) is absent
// from the summary but present in the base, which is exactly what a refresh job
// that has not run since the UTC day rolled over leaves behind. A read that
// splits at today serves yesterday from a summary that has no row for it and
// reports zero; a read that splits at the summary's own coverage serves it from
// the base and matches.
func seedRehearsal(t *testing.T, db *sql.DB, today time.Time) {
	t.Helper()

	exec := func(query string, args ...any) {
		t.Helper()
		if len(args) > 0 {
			query = fmt.Sprintf(query, args...)
		}
		_, err := db.Exec(query)
		require.NoError(t, err, "seed: %s", query)
	}

	id := rehearsalProject.String()
	for d := rehearsalDays; d >= 0; d-- {
		day := today.AddDate(0, 0, -d).Format("2006-01-02")
		// Each day gets a different number of distinct subjects, so a day
		// served from the wrong half shows up as a wrong value, not just a
		// missing row.
		n := 10 + d

		var users, docs, channels, clients, sessions []string
		for i := range n {
			users = append(users, fmt.Sprintf("('%s','u%d','%s %02d:00:00','user-activated','web')", id, i, day, i%24))
			docs = append(docs, fmt.Sprintf("('%s','doc%d','a%d','%s %02d:00:00','document-attached')", id, i, i%3, day, i%24))
			channels = append(channels, fmt.Sprintf("('%s','ch%d','%s %02d:00:00','channel-joined')", id, i, day, i%24))
			clients = append(clients, fmt.Sprintf("('%s','c%d','%s %02d:00:00','client-activated')", id, i, day, i%24))
			// Sessions are unique per day and spread over three channels, so
			// the sessions total exercises the union across channels and peak
			// exercises the per-channel maximum.
			sessions = append(sessions, fmt.Sprintf("('%s','s%s-%d','%s %02d:00:00','u%d','ch%d','session-started')",
				id, day, i, day, i%24, i%3, i%3))
		}

		exec("INSERT INTO user_events (project_id,user_id,timestamp,event_type,user_agent) VALUES %s",
			strings.Join(users, ","))
		exec("INSERT INTO document_events (project_id,document_key,actor_id,timestamp,event_type) VALUES %s",
			strings.Join(docs, ","))
		exec("INSERT INTO channel_events (project_id,channel_key,timestamp,event_type) VALUES %s",
			strings.Join(channels, ","))
		exec("INSERT INTO client_events (project_id,client_id,timestamp,event_type) VALUES %s",
			strings.Join(clients, ","))
		exec("INSERT INTO session_events (project_id,session_id,timestamp,user_id,channel_key,event_type) VALUES %s",
			strings.Join(sessions, ","))
	}

	// Fill the summaries the way the refresh job does, but stopping a day short
	// of the running day rather than at it.
	from := dayFmt(today.AddDate(0, 0, -rehearsalDays))
	through := dayFmt(today.AddDate(0, 0, -1))
	exec(`INSERT INTO sum_user_hll_daily SELECT project_id, DATE(timestamp), HLL_UNION(HLL_HASH(user_id))
	      FROM user_events WHERE project_id = '%s' AND DATE(timestamp) >= '%s' AND DATE(timestamp) < '%s'
	      GROUP BY project_id, DATE(timestamp)`, id, from, through)
	exec(`INSERT INTO sum_document_hll_daily SELECT project_id, DATE(timestamp), HLL_UNION(HLL_HASH(document_key))
	      FROM document_events WHERE project_id = '%s' AND DATE(timestamp) >= '%s' AND DATE(timestamp) < '%s'
	      GROUP BY project_id, DATE(timestamp)`, id, from, through)
	exec(`INSERT INTO sum_channel_hll_daily SELECT project_id, DATE(timestamp), HLL_UNION(HLL_HASH(channel_key))
	      FROM channel_events WHERE project_id = '%s' AND DATE(timestamp) >= '%s' AND DATE(timestamp) < '%s'
	      GROUP BY project_id, DATE(timestamp)`, id, from, through)
	exec(`INSERT INTO sum_session_hll_daily_ch
	      SELECT project_id, DATE(timestamp), channel_key, HLL_UNION(HLL_HASH(session_id))
	      FROM session_events WHERE project_id = '%s' AND DATE(timestamp) >= '%s' AND DATE(timestamp) < '%s'
	      GROUP BY project_id, DATE(timestamp), channel_key`, id, from, through)
	exec(`INSERT INTO sum_client_hll_daily SELECT project_id, event_type, DATE(timestamp), HLL_UNION(HLL_HASH(client_id))
	      FROM client_events WHERE project_id = '%s' AND DATE(timestamp) >= '%s' AND DATE(timestamp) < '%s'
	      GROUP BY project_id, event_type, DATE(timestamp)`, id, from, through)
}

// requireLaggingCoverage skips unless every summary table stops at wantMaxDt.
// The split boundary is global, not per project, so a cluster carrying other
// projects' summaries past that day cannot stage the refresh-lag scenario.
func requireLaggingCoverage(t *testing.T, db *sql.DB, wantMaxDt time.Time) {
	t.Helper()

	for _, d := range allDescs {
		var maxDt sql.NullString
		row := db.QueryRow(fmt.Sprintf("SELECT MAX(dt) FROM %s", d.summaryTable))
		require.NoError(t, row.Scan(&maxDt), "read coverage of %s", d.summaryTable)
		require.True(t, maxDt.Valid, "%s is empty; seeding did not run", d.summaryTable)

		if maxDt.String != dayFmt(wantMaxDt) {
			t.Skipf("%s covers through %s, want %s: point SR_DSN at a cluster whose "+
				"summaries this test owns", d.summaryTable, maxDt.String, dayFmt(wantMaxDt))
		}
	}
}

// TestE2EDualReadMatchesBaseUnderRefreshLag is the regression test for the
// refresh-lag gap: with the summary a day behind, every metric read through the
// dual read must equal what the base-only path returns. Before the split moved
// to the summary's actual coverage, the day the summary had not reached yet was
// served from neither half and came back as zero.
func TestE2EDualReadMatchesBaseUnderRefreshLag(t *testing.T) {
	dsn := os.Getenv("SR_DSN")
	if dsn == "" {
		t.Skip("set SR_DSN to run the StarRocks e2e rehearsal")
	}

	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, db.Ping())

	today := todayUTC()
	seedRehearsal(t, db, today)
	requireLaggingCoverage(t, db, today.AddDate(0, 0, -2))

	off, err := Ensure(&Config{DSN: dsn, SummaryEnabled: false})
	require.NoError(t, err)
	defer func() { _ = off.Close() }()
	on, err := Ensure(&Config{DSN: dsn, SummaryEnabled: true})
	require.NoError(t, err)
	defer func() { _ = on.Close() }()

	ctx := context.Background()
	id := rehearsalProject
	// The window straddles the boundary: it starts inside the summary's
	// coverage and runs past the day the summary is missing, into today.
	from := today.AddDate(0, 0, -rehearsalDays)
	to := today.AddDate(0, 0, 1)

	for name, fn := range map[string]func(Warehouse) (int, error){
		"active users":     func(w Warehouse) (int, error) { return w.GetActiveUsersCount(ctx, id, from, to) },
		"active documents": func(w Warehouse) (int, error) { return w.GetActiveDocumentsCount(ctx, id, from, to) },
		"active channels":  func(w Warehouse) (int, error) { return w.GetActiveChannelsCount(ctx, id, from, to) },
		"active clients":   func(w Warehouse) (int, error) { return w.GetActiveClientsCount(ctx, id, from, to) },
		"sessions":         func(w Warehouse) (int, error) { return w.GetSessionsCount(ctx, id, from, to) },
		"peak":             func(w Warehouse) (int, error) { return w.GetPeakSessionsPerChannelCount(ctx, id, from, to) },
	} {
		t.Run("count "+name, func(t *testing.T) {
			base, err := fn(off)
			require.NoError(t, err)
			dual, err := fn(on)
			require.NoError(t, err)
			require.NotZero(t, base, "base-only read is empty; seeding did not take")
			assert.Equal(t, base, dual, "dual read must equal base-only")
		})
	}

	for name, fn := range map[string]func(Warehouse) ([]types.MetricPoint, error){
		"active users":     func(w Warehouse) ([]types.MetricPoint, error) { return w.GetActiveUsers(ctx, id, from, to) },
		"active documents": func(w Warehouse) ([]types.MetricPoint, error) { return w.GetActiveDocuments(ctx, id, from, to) },
		"active channels":  func(w Warehouse) ([]types.MetricPoint, error) { return w.GetActiveChannels(ctx, id, from, to) },
		"active clients":   func(w Warehouse) ([]types.MetricPoint, error) { return w.GetActiveClients(ctx, id, from, to) },
		"sessions":         func(w Warehouse) ([]types.MetricPoint, error) { return w.GetSessions(ctx, id, from, to) },
		"peak": func(w Warehouse) ([]types.MetricPoint, error) {
			return w.GetPeakSessionsPerChannel(ctx, id, from, to)
		},
	} {
		t.Run("series "+name, func(t *testing.T) {
			base, err := fn(off)
			require.NoError(t, err)
			dual, err := fn(on)
			require.NoError(t, err)
			// Every seeded day must be present. The gap dropped one of them
			// entirely, which the dashboard drew as a zero.
			assert.Len(t, base, rehearsalDays+1, "base-only series is short; seeding did not take")
			assert.Equal(t, base, dual, "dual read must equal base-only, day for day")
		})
	}
}

// TestE2EDualReadStaysOnTheRollups is the regression test for the two latency
// causes: the fresh half must plan onto the sync MV rather than the raw event
// table, and the sessions total and series must read the coarse
// rl_session_daily rollup rather than every channel-day sketch. Both are
// invisible to a query-string assertion — the same SQL falls back to a base
// scan on a cluster whose rollups are missing or whose optimizer stops matching
// the shape.
func TestE2EDualReadStaysOnTheRollups(t *testing.T) {
	dsn := os.Getenv("SR_DSN")
	if dsn == "" {
		t.Skip("set SR_DSN to run the StarRocks e2e rehearsal")
	}

	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, db.Ping())

	today := todayUTC()
	seedRehearsal(t, db, today)

	// rollups reports which index every scan in the plan reads.
	rollups := func(t *testing.T, query string) []string {
		t.Helper()
		rows, err := db.Query("EXPLAIN " + query)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()

		var picked []string
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			if _, after, found := strings.Cut(line, "rollup: "); found {
				picked = append(picked, strings.TrimSpace(after))
			}
		}
		require.NoError(t, rows.Err())
		require.NotEmpty(t, picked, "no scan node in plan for %s", query)
		return picked
	}

	id := rehearsalProject
	from := today.AddDate(0, 0, -rehearsalDays)
	to := today.AddDate(0, 0, 1)
	split := today.AddDate(0, 0, -1)

	type plan struct {
		name  string
		query string
		base  string
		want  string
	}
	var plans []plan
	for _, d := range allDescs {
		plans = append(plans,
			plan{"series " + d.baseTable, d.seriesQuery(id, from, to, split), d.baseTable, ""},
			plan{"total " + d.baseTable, d.totalQuery(id, from, to, split), d.baseTable, ""},
		)
	}
	plans = append(plans,
		plan{"peak series", descSession.peakSeriesQuery(id, from, to, split), descSession.baseTable, ""},
		plan{"peak total", descSession.peakTotalQuery(id, from, to, split), descSession.baseTable, ""},
		// The sessions total and series must not pay for the channel_key
		// dimension they never read.
		plan{"sessions series on the coarse rollup", descSession.seriesQuery(id, from, to, split), "", "rl_session_daily"},
		plan{"sessions total on the coarse rollup", descSession.totalQuery(id, from, to, split), "", "rl_session_daily"},
	)

	for _, p := range plans {
		t.Run(p.name, func(t *testing.T) {
			picked := rollups(t, p.query)
			if p.base != "" {
				assert.NotContains(t, picked, p.base,
					"fresh half fell back to a raw scan of %s; plan reads %v", p.base, picked)
			}
			if p.want != "" {
				assert.Contains(t, picked, p.want, "plan reads %v", picked)
			}
		})
	}

	// Peak is the one metric that does need channel_key, so it keeps the base
	// index of the summary rather than the coarse rollup.
	assert.Contains(t, rollups(t, descSession.peakSeriesQuery(id, from, to, split)),
		descSession.summaryTable, "peak must keep the per-channel index")
}

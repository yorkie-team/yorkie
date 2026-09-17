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
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
)

func TestCoverageBoundary(t *testing.T) {
	from, today := day("2026-08-01"), day("2026-09-07")

	tests := []struct {
		name  string
		maxDt time.Time
		want  time.Time
	}{
		{
			// The refresh job ran today, so the summary holds yesterday: the
			// boundary is today, the value the dual read has always assumed.
			name:  "refreshed through yesterday",
			maxDt: day("2026-09-06"),
			want:  day("2026-09-07"),
		},
		{
			// A once-daily job that has not run since the day rolled over
			// leaves the summary a day behind. Yesterday must come from the
			// base, not from a summary that has no row for it.
			name:  "one day behind",
			maxDt: day("2026-09-05"),
			want:  day("2026-09-06"),
		},
		{
			name:  "several days behind after a missed run",
			maxDt: day("2026-09-01"),
			want:  day("2026-09-02"),
		},
		{
			// A backfill that included the running day leaves a partial today
			// in the summary. Serving it would undercount today, so the
			// boundary never passes today.
			name:  "already holds today",
			maxDt: day("2026-09-07"),
			want:  day("2026-09-07"),
		},
		{
			name:  "clock skew puts a future day in the summary",
			maxDt: day("2026-09-20"),
			want:  day("2026-09-07"),
		},
		{
			// An empty summary covers nothing, so the whole window comes from
			// the base.
			name:  "empty summary",
			maxDt: time.Time{},
			want:  from,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, coverageBoundary(tc.maxDt, today, from))
		})
	}
}

func TestCoverageCacheProbesOncePerWindow(t *testing.T) {
	var cache coverageCache

	calls := 0
	fetch := func(context.Context) (map[string]time.Time, error) {
		calls++
		return map[string]time.Time{"sum_user_hll_daily": day("2026-09-05")}, nil
	}

	ctx := context.Background()
	for range 3 {
		got, err := cache.maxDay(ctx, "sum_user_hll_daily", fetch)
		require.NoError(t, err)
		assert.Equal(t, day("2026-09-05"), got)
	}
	assert.Equal(t, 1, calls, "a dashboard load must probe once, not once per metric")

	// Another table from the same probe is answered from the same result.
	_, err := cache.maxDay(ctx, "sum_session_hll_daily_ch", fetch)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)

	cache.fetchedAt = time.Now().Add(-coverageTTL - time.Second)
	_, err = cache.maxDay(ctx, "sum_user_hll_daily", fetch)
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "the probe must be redone once the TTL is up")
}

func TestCoverageCacheUnknownTableCoversNothing(t *testing.T) {
	var cache coverageCache
	fetch := func(context.Context) (map[string]time.Time, error) {
		return map[string]time.Time{}, nil
	}

	got, err := cache.maxDay(context.Background(), "sum_user_hll_daily", fetch)
	require.NoError(t, err)
	assert.True(t, got.IsZero())
}

func TestCoverageCachePropagatesProbeError(t *testing.T) {
	var cache coverageCache
	want := errors.New("boom")
	calls := 0
	fetch := func(context.Context) (map[string]time.Time, error) {
		calls++
		return nil, want
	}

	_, err := cache.maxDay(context.Background(), "sum_user_hll_daily", fetch)
	assert.ErrorIs(t, err, want)

	// A failed probe must not be cached as "covers nothing".
	_, err = cache.maxDay(context.Background(), "sum_user_hll_daily", fetch)
	assert.ErrorIs(t, err, want)
	assert.Equal(t, 2, calls)
}

func TestCoverageQueryReadsEverySummaryTable(t *testing.T) {
	got := norm(coverageQuery())

	for _, d := range allDescs {
		assert.Contains(t, got, "MAX(dt) AS max_dt FROM "+d.summaryTable)
		assert.Contains(t, got, "'"+d.summaryTable+"' AS summary_table")
	}
	assert.Equal(t, len(allDescs)-1, strings.Count(got, "UNION ALL"), "one round trip")
}

// Every dual-read metric must go through the coverage probe: a metric that
// still split at a fixed today would answer without probing, and would put the
// refresh-lag gap back for that metric alone. Pointing the driver at a closed
// port makes the probe fail, so a missing error is a missing probe.
func TestEveryMetricProbesCoverage(t *testing.T) {
	// sql.Open does not connect, so the failure lands on the probe's query.
	driver, err := sql.Open("mysql", "root:@tcp(127.0.0.1:1)/yorkie?timeout=200ms")
	require.NoError(t, err)
	r := &StarRocks{conf: &Config{SummaryEnabled: true}, driver: driver}
	defer func() { _ = r.Close() }()

	ctx := context.Background()
	id := types.ID("p1")
	from, to := day("2026-08-01"), day("2026-09-01")

	counts := map[string]func() (int, error){
		"active users":     func() (int, error) { return r.GetActiveUsersCount(ctx, id, from, to) },
		"active documents": func() (int, error) { return r.GetActiveDocumentsCount(ctx, id, from, to) },
		"active clients":   func() (int, error) { return r.GetActiveClientsCount(ctx, id, from, to) },
		"active channels":  func() (int, error) { return r.GetActiveChannelsCount(ctx, id, from, to) },
		"sessions":         func() (int, error) { return r.GetSessionsCount(ctx, id, from, to) },
	}
	for name, fn := range counts {
		t.Run("count "+name, func(t *testing.T) {
			_, err := fn()
			require.ErrorContains(t, err, "query summary coverage")
		})
	}

	series := map[string]func() ([]types.MetricPoint, error){
		"active users":     func() ([]types.MetricPoint, error) { return r.GetActiveUsers(ctx, id, from, to) },
		"active documents": func() ([]types.MetricPoint, error) { return r.GetActiveDocuments(ctx, id, from, to) },
		"active clients":   func() ([]types.MetricPoint, error) { return r.GetActiveClients(ctx, id, from, to) },
		"active channels":  func() ([]types.MetricPoint, error) { return r.GetActiveChannels(ctx, id, from, to) },
		"sessions":         func() ([]types.MetricPoint, error) { return r.GetSessions(ctx, id, from, to) },
		"peak":             func() ([]types.MetricPoint, error) { return r.GetPeakSessionsPerChannel(ctx, id, from, to) },
	}
	for name, fn := range series {
		t.Run("series "+name, func(t *testing.T) {
			_, err := fn()
			require.ErrorContains(t, err, "query summary coverage")
		})
	}
}

// captureConnector answers every query with an error after recording its SQL,
// so a read's built query can be asserted on without a StarRocks to run it
// against. It is a connector rather than a registered driver so the tests need
// no global driver name.
type captureConnector struct {
	queries []string
}

// errCaptured is what the fake connection answers with. It is deliberately not
// driver.ErrBadConn, which database/sql would retry on a fresh connection.
var errCaptured = errors.New("captured")

func (c *captureConnector) Connect(context.Context) (driver.Conn, error) {
	return &captureConn{c: c}, nil
}
func (c *captureConnector) Driver() driver.Driver { return captureDriver{} }

type captureDriver struct{}

func (captureDriver) Open(string) (driver.Conn, error) { return nil, errCaptured }

type captureConn struct {
	c *captureConnector
}

func (c *captureConn) Prepare(string) (driver.Stmt, error) { return nil, errCaptured }
func (c *captureConn) Close() error                        { return nil }
func (c *captureConn) Begin() (driver.Tx, error)           { return nil, errCaptured }

func (c *captureConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.c.queries = append(c.c.queries, query)
	return nil, errCaptured
}

// Peak reads its own summary table, which a separate refresh job fills and
// which may therefore lag sum_session_hll_daily_ch. Splitting the window on the
// sketch table's coverage would serve days out of sum_session_peak_daily that
// nothing has written yet, and the dashboard would draw them as zeroes. The
// split and the table read must be the same metric's.
func TestPeakReadSplitsOnThePeakSummary(t *testing.T) {
	conn := &captureConnector{}
	r := &StarRocks{conf: &Config{SummaryEnabled: true}, driver: sql.OpenDB(conn)}
	defer func() { _ = r.Close() }()

	today := todayUTC()
	peakMaxDt := today.AddDate(0, 0, -4)
	// A primed, fresh probe: no round trip, and the peak summary sits two days
	// behind the per-channel sketch summary.
	r.coverage.maxDays = map[string]time.Time{
		descSession.summaryTable: today.AddDate(0, 0, -2),
		descPeak.summaryTable:    peakMaxDt,
	}
	r.coverage.fetchedAt = time.Now()

	from, to := today.AddDate(0, 0, -30), today.AddDate(0, 0, 1)
	_, err := r.GetPeakSessionsPerChannel(context.Background(), types.ID("p1"), from, to)
	require.ErrorIs(t, err, errCaptured)
	require.Len(t, conn.queries, 1, "the primed probe must not have cost a round trip")

	got := norm(conn.queries[0])
	split := dayFmt(peakMaxDt.AddDate(0, 0, 1))
	assert.Contains(t, got, "FROM sum_session_peak_daily WHERE project_id = 'p1' "+
		"AND dt >= '"+dayFmt(from)+"' AND dt < '"+split+"'")
	assert.NotContains(t, got, descSession.summaryTable)
	// The two days the peak summary has not reached come from the base.
	assert.Contains(t, got, "DATE(timestamp) >= '"+split+"'")
}

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
		"peak":             func() (int, error) { return r.GetPeakSessionsPerChannelCount(ctx, id, from, to) },
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

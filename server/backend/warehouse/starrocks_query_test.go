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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
)

// norm collapses all runs of whitespace to single spaces so assertions do not
// depend on the query's formatting.
func norm(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func day(s string) time.Time {
	d, _ := time.Parse("2006-01-02", s)
	return d
}

// covUntil is the steady state: a summary filled from long before any window
// these tests ask for up to boundary. No window reaches below its first day, so
// no query below emits a pre-range branch.
func covUntil(boundary string) dayRange {
	return newDayRange(day("2000-01-01"), day(boundary))
}

func TestTotalQueryStraddlingSummary(t *testing.T) {
	got := norm(descUser.totalQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), covUntil("2026-08-31")))

	assert.Contains(t, got, "SELECT HLL_UNION_AGG(sketch) FROM")
	assert.Contains(t, got, "SELECT user_hll AS sketch FROM sum_user_hll_daily "+
		"WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31'")
	assert.Contains(t, got, "UNION ALL")
	assert.Contains(t, got, "SELECT HLL_UNION(HLL_HASH(user_id)) AS sketch FROM user_events "+
		"WHERE project_id = 'p1' AND DATE(timestamp) >= '2026-08-31' AND DATE(timestamp) < '2026-09-01' "+
		"GROUP BY DATE(timestamp)")
	// a per-row sketch here would leave the HLL_UNION_AGG above the UNION ALL,
	// where it cannot rewrite onto the MV; see totalQuery.
	assert.NotContains(t, got, "SELECT HLL_HASH(user_id) AS sketch")
}

func TestSeriesQueryEntirelyPastSummaryOnly(t *testing.T) {
	got := norm(descDocument.seriesQuery(types.ID("p1"), day("2026-08-01"), day("2026-08-31"), covUntil("2026-08-31")))

	assert.Contains(t, got, "SELECT dt AS event_date, HLL_UNION_AGG(document_hll) AS metric_value "+
		"FROM sum_document_hll_daily WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31' GROUP BY dt")
	assert.Contains(t, got, "ORDER BY event_date ASC")
	assert.NotContains(t, got, "UNION ALL")
	assert.NotContains(t, got, "document_events")
}

func TestSeriesQueryEntirelyTodayBaseOnly(t *testing.T) {
	got := norm(descUser.seriesQuery(types.ID("p1"), day("2026-08-31"), day("2026-09-01"), covUntil("2026-08-31")))

	assert.Contains(t, got, "APPROX_COUNT_DISTINCT(user_id) AS metric_value FROM user_events")
	assert.NotContains(t, got, "sum_user_hll_daily")
	assert.NotContains(t, got, "UNION ALL")
}

func TestTotalQueryClientCarriesEventType(t *testing.T) {
	got := norm(descClient.totalQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), covUntil("2026-08-31")))

	// summary half keyed by event_type
	assert.Contains(t, got, "SELECT client_hll AS sketch FROM sum_client_hll_daily "+
		"WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31' AND event_type = 'client-activated'")
	// fresh half filters event_type too
	assert.Contains(t, got, "HLL_UNION(HLL_HASH(client_id)) AS sketch FROM client_events")
	assert.Contains(t, got, "AND event_type = 'client-activated'")
}

func TestPeakSeriesQueryStraddling(t *testing.T) {
	got := norm(descPeak.peakSeriesQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), covUntil("2026-08-31")))

	assert.Contains(t, got, "SELECT event_date, metric_value FROM")
	// The history half reads the precomputed daily peak, one plain integer per
	// project-day, not the per-channel sketches it used to reduce itself.
	assert.Contains(t, got, "SELECT dt AS event_date, MAX(peak_sessions) AS metric_value "+
		"FROM sum_session_peak_daily WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31' "+
		"GROUP BY dt")
	assert.NotContains(t, got, "sum_session_hll_daily_ch")
	assert.NotContains(t, got, "HLL_UNION_AGG(session_hll)")
	// The fresh half has no precomputed row, so it still reduces per
	// (day, channel) in-branch and takes the daily maximum itself.
	assert.Contains(t, got, "SELECT DATE(timestamp) AS event_date, channel_key, "+
		"APPROX_COUNT_DISTINCT(session_id) AS session_count FROM session_events")
	assert.Contains(t, got, "GROUP BY DATE(timestamp), channel_key")
	assert.Contains(t, got, "MAX(session_count) AS metric_value")
	assert.Contains(t, got, "UNION ALL")
	assert.Contains(t, got, "ORDER BY event_date ASC")
}

// The summary half must never reach for channel_key: the whole point of the
// precomputed table is that the per-channel reduction already happened offline.
func TestPeakSeriesQueryHistoryOnlyIgnoresChannels(t *testing.T) {
	got := norm(descPeak.peakSeriesQuery(types.ID("p1"), day("2026-08-01"), day("2026-08-31"), covUntil("2026-08-31")))

	assert.Contains(t, got, "FROM sum_session_peak_daily")
	assert.NotContains(t, got, "channel_key")
	assert.NotContains(t, got, "session_events")
	assert.NotContains(t, got, "UNION ALL")
}

func TestSeriesQueryStraddlingConcatenatesHalves(t *testing.T) {
	got := norm(descUser.seriesQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), covUntil("2026-08-31")))

	// history from the summary, per day
	assert.Contains(t, got, "SELECT dt AS event_date, HLL_UNION_AGG(user_hll) AS metric_value "+
		"FROM sum_user_hll_daily WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31' GROUP BY dt")
	// today from the base, per day
	assert.Contains(t, got, "SELECT DATE(timestamp) AS event_date, APPROX_COUNT_DISTINCT(user_id) AS metric_value "+
		"FROM user_events")
	assert.Contains(t, got, "DATE(timestamp) >= '2026-08-31' AND DATE(timestamp) < '2026-09-01'")
	assert.Contains(t, got, "GROUP BY DATE(timestamp)")
	assert.Contains(t, got, "UNION ALL")
	assert.Contains(t, got, "ORDER BY event_date ASC")
}

func TestSessionTotalUnionsAcrossChannels(t *testing.T) {
	got := norm(descSession.totalQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), covUntil("2026-08-31")))

	// distinct sessions across the whole window: union every channel-day sketch,
	// cardinality once. The summary half must NOT filter or group by channel_key.
	assert.Contains(t, got, "SELECT HLL_UNION_AGG(sketch) FROM")
	assert.Contains(t, got, "SELECT session_hll AS sketch FROM sum_session_hll_daily_ch "+
		"WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31'")
	assert.Contains(t, got, "SELECT HLL_UNION(HLL_HASH(session_id)) AS sketch FROM session_events")
	assert.NotContains(t, got, "channel_key")
}

func TestTotalQueryEmptyWindowNoUnion(t *testing.T) {
	got := norm(descUser.totalQuery(types.ID("p1"), day("2026-08-31"), day("2026-08-31"), covUntil("2026-08-31")))

	// from == to: a single summary select over an empty range, no UNION ALL, no panic
	assert.Contains(t, got, "sum_user_hll_daily")
	assert.NotContains(t, got, "UNION ALL")
}

// The fresh half must reference timestamp only through DATE(timestamp). A raw
// timestamp bound keeps the sync MV (mv_*_hll_daily, which carries only
// mv_dt = DATE(timestamp)) out of the plan and falls back to a full scan of the
// base event table. Verified with EXPLAIN on StarRocks 3.3: with the raw bound
// the plan reads "rollup: client_events", without it "rollup:
// mv_client_hll_daily".
func TestFreshHalfOmitsRawTimestampBounds(t *testing.T) {
	from, to := day("2026-08-01"), day("2026-09-01")
	cov := covUntil("2026-08-31")
	queries := map[string]string{
		"series":      descUser.seriesQuery(types.ID("p1"), from, to, cov),
		"total":       descUser.totalQuery(types.ID("p1"), from, to, cov),
		"peak series": descPeak.peakSeriesQuery(types.ID("p1"), from, to, cov),
	}
	for name, q := range queries {
		t.Run(name, func(t *testing.T) {
			got := norm(q)
			assert.NotContains(t, got, "AND timestamp >=", "raw bound defeats the MV rewrite")
			assert.NotContains(t, got, "AND timestamp <", "raw bound defeats the MV rewrite")
			assert.Contains(t, got, "DATE(timestamp) >= '2026-08-31'")
			assert.Contains(t, got, "DATE(timestamp) < '2026-09-01'")
		})
	}
}

// A summary filled only for the last few days of the window — the shape of a
// table added to a cluster where the dual read is already on, whose refresh
// job's 7-day lookback ran before the one-time backfill. MAX(dt) alone would
// call the whole window covered and serve the days below the first row from a
// summary that has no rows for them, drawing them as zeroes. They come from the
// base instead, in a branch shaped exactly like the fresh one.
func TestSeriesQueryBelowSummaryFloorReadsBase(t *testing.T) {
	cov := newDayRange(day("2026-08-25"), day("2026-08-31"))
	got := norm(descUser.seriesQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), cov))

	// the days below the summary's first row, from the base
	assert.Contains(t, got, "SELECT DATE(timestamp) AS event_date, APPROX_COUNT_DISTINCT(user_id) AS metric_value "+
		"FROM user_events WHERE project_id = 'p1' "+
		"AND DATE(timestamp) >= '2026-08-01' AND DATE(timestamp) < '2026-08-25' GROUP BY DATE(timestamp)")
	// the covered days, from the summary
	assert.Contains(t, got, "FROM sum_user_hll_daily WHERE project_id = 'p1' "+
		"AND dt >= '2026-08-25' AND dt < '2026-08-31'")
	// the days above the summary's last row, from the base
	assert.Contains(t, got, "AND DATE(timestamp) >= '2026-08-31' AND DATE(timestamp) < '2026-09-01'")
	assert.Equal(t, 2, strings.Count(got, "UNION ALL"), "three branches")
	// The summary must never be asked for a day it does not hold.
	assert.NotContains(t, got, "dt >= '2026-08-01'")
}

// The total's pre-range branch has to be a sketch branch like the fresh one: a
// per-row HLL_HASH projection would leave the outer HLL_UNION_AGG above the
// UNION ALL, off the sync MV, and full-scan the base. See totalQuery.
func TestTotalQueryBelowSummaryFloorUnionsBaseSketch(t *testing.T) {
	cov := newDayRange(day("2026-08-25"), day("2026-08-31"))
	got := norm(descUser.totalQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), cov))

	assert.Contains(t, got, "SELECT HLL_UNION(HLL_HASH(user_id)) AS sketch FROM user_events "+
		"WHERE project_id = 'p1' AND DATE(timestamp) >= '2026-08-01' AND DATE(timestamp) < '2026-08-25' "+
		"GROUP BY DATE(timestamp)")
	assert.Contains(t, got, "SELECT user_hll AS sketch FROM sum_user_hll_daily "+
		"WHERE project_id = 'p1' AND dt >= '2026-08-25' AND dt < '2026-08-31'")
	assert.Contains(t, got, "SELECT HLL_UNION(HLL_HASH(user_id)) AS sketch FROM user_events "+
		"WHERE project_id = 'p1' AND DATE(timestamp) >= '2026-08-31' AND DATE(timestamp) < '2026-09-01' "+
		"GROUP BY DATE(timestamp)")
	assert.Equal(t, 2, strings.Count(got, "UNION ALL"), "three branches")
	assert.NotContains(t, got, "SELECT HLL_HASH(user_id) AS sketch")
	// Cardinality is still taken exactly once, over the union of all three.
	assert.Equal(t, 1, strings.Count(got, "HLL_UNION_AGG"))
}

// Peak's pre-range branch reduces per (day, channel) and takes the daily MAX
// itself, exactly as its fresh half does: neither has a precomputed row.
func TestPeakSeriesQueryBelowSummaryFloorReadsBase(t *testing.T) {
	cov := newDayRange(day("2026-08-25"), day("2026-08-31"))
	got := norm(descPeak.peakSeriesQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), cov))

	assert.Contains(t, got, "SELECT event_date, MAX(session_count) AS metric_value FROM "+
		"(SELECT DATE(timestamp) AS event_date, channel_key, APPROX_COUNT_DISTINCT(session_id) AS session_count "+
		"FROM session_events WHERE project_id = 'p1' "+
		"AND DATE(timestamp) >= '2026-08-01' AND DATE(timestamp) < '2026-08-25' "+
		"GROUP BY DATE(timestamp), channel_key) fc GROUP BY event_date")
	assert.Contains(t, got, "FROM sum_session_peak_daily WHERE project_id = 'p1' "+
		"AND dt >= '2026-08-25' AND dt < '2026-08-31'")
	assert.Equal(t, 2, strings.Count(got, "UNION ALL"), "three branches")
	assert.Equal(t, 2, strings.Count(got, "GROUP BY DATE(timestamp), channel_key"), "both base branches")
}

// A window entirely below the summary's first day is base-only. The summary
// half must not appear at all: an empty range over it would read as zeroes.
func TestQueriesEntirelyBelowSummaryFloorAreBaseOnly(t *testing.T) {
	cov := newDayRange(day("2026-08-25"), day("2026-08-31"))
	from, to := day("2026-08-01"), day("2026-08-10")
	queries := map[string]string{
		"series":      descUser.seriesQuery(types.ID("p1"), from, to, cov),
		"total":       descUser.totalQuery(types.ID("p1"), from, to, cov),
		"peak series": descPeak.peakSeriesQuery(types.ID("p1"), from, to, cov),
	}
	for name, q := range queries {
		t.Run(name, func(t *testing.T) {
			got := norm(q)
			assert.NotContains(t, got, "UNION ALL")
			assert.NotContains(t, got, "sum_")
			assert.Contains(t, got, "DATE(timestamp) >= '2026-08-01'")
			assert.Contains(t, got, "DATE(timestamp) < '2026-08-10'")
		})
	}
}

// An empty coverage — the summary holds nothing — is base-only for every
// builder, which is what a cluster whose summaries are not backfilled yet gets.
func TestQueriesEmptyCoverageAreBaseOnly(t *testing.T) {
	from, to := day("2026-08-01"), day("2026-09-01")
	queries := map[string]string{
		"series":      descUser.seriesQuery(types.ID("p1"), from, to, dayRange{Empty: true}),
		"total":       descUser.totalQuery(types.ID("p1"), from, to, dayRange{Empty: true}),
		"peak series": descPeak.peakSeriesQuery(types.ID("p1"), from, to, dayRange{Empty: true}),
	}
	for name, q := range queries {
		t.Run(name, func(t *testing.T) {
			got := norm(q)
			assert.NotContains(t, got, "UNION ALL")
			assert.NotContains(t, got, "sum_")
			assert.Contains(t, got, "DATE(timestamp) >= '2026-08-01'")
			assert.Contains(t, got, "DATE(timestamp) < '2026-09-01'")
		})
	}
}

// The steady state pinned whole: a window that starts inside the summary's
// coverage emits the two branches it always has, in the order it always has,
// with no pre-range branch. The pre range is an addition to the edge case, not
// a change to the path every dashboard load takes, and a diff here means that
// path moved.
func TestSteadyStateSQLIsUnchanged(t *testing.T) {
	from, to := day("2026-08-01"), day("2026-09-01")
	cov := covUntil("2026-08-31")
	id := types.ID("p1")

	assert.Equal(t, "SELECT event_date, metric_value FROM (\n"+
		"SELECT dt AS event_date, HLL_UNION_AGG(user_hll) AS metric_value FROM sum_user_hll_daily "+
		"WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31' GROUP BY dt\n"+
		"UNION ALL\n"+
		"SELECT DATE(timestamp) AS event_date, APPROX_COUNT_DISTINCT(user_id) AS metric_value FROM user_events "+
		"WHERE project_id = 'p1' AND DATE(timestamp) >= '2026-08-31' AND DATE(timestamp) < '2026-09-01' "+
		"GROUP BY DATE(timestamp)\n"+
		") t ORDER BY event_date ASC;", descUser.seriesQuery(id, from, to, cov))

	assert.Equal(t, "SELECT HLL_UNION_AGG(sketch) FROM (\n"+
		"SELECT client_hll AS sketch FROM sum_client_hll_daily "+
		"WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31' "+
		"AND event_type = 'client-activated'\n"+
		"UNION ALL\n"+
		"SELECT HLL_UNION(HLL_HASH(client_id)) AS sketch FROM client_events "+
		"WHERE project_id = 'p1' AND DATE(timestamp) >= '2026-08-31' AND DATE(timestamp) < '2026-09-01' "+
		"AND event_type = 'client-activated' GROUP BY DATE(timestamp)\n"+
		") t;", descClient.totalQuery(id, from, to, cov))

	assert.Equal(t, "SELECT event_date, metric_value FROM (\n"+
		"SELECT dt AS event_date, MAX(peak_sessions) AS metric_value FROM sum_session_peak_daily "+
		"WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31' GROUP BY dt\n"+
		"UNION ALL\n"+
		"SELECT event_date, MAX(session_count) AS metric_value FROM ("+
		"SELECT DATE(timestamp) AS event_date, channel_key, APPROX_COUNT_DISTINCT(session_id) AS session_count "+
		"FROM session_events WHERE project_id = 'p1' "+
		"AND DATE(timestamp) >= '2026-08-31' AND DATE(timestamp) < '2026-09-01' "+
		"GROUP BY DATE(timestamp), channel_key) fc GROUP BY event_date\n"+
		") t ORDER BY event_date ASC;", descPeak.peakSeriesQuery(id, from, to, cov))
}

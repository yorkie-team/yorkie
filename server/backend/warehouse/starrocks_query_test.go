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

func TestTotalQueryStraddlingSummary(t *testing.T) {
	got := norm(descUser.totalQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), day("2026-08-31")))

	assert.Contains(t, got, "SELECT HLL_UNION_AGG(sketch) FROM")
	assert.Contains(t, got, "SELECT user_hll AS sketch FROM sum_user_hll_daily "+
		"WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31'")
	assert.Contains(t, got, "UNION ALL")
	assert.Contains(t, got, "SELECT HLL_UNION(HLL_HASH(user_id)) AS sketch FROM user_events "+
		"WHERE project_id = 'p1' AND DATE(timestamp) >= '2026-08-31' AND DATE(timestamp) < '2026-09-01' "+
		"GROUP BY DATE(timestamp)")
}

func TestSeriesQueryEntirelyPastSummaryOnly(t *testing.T) {
	got := norm(descDocument.seriesQuery(types.ID("p1"), day("2026-08-01"), day("2026-08-31"), day("2026-08-31")))

	assert.Contains(t, got, "SELECT dt AS event_date, HLL_UNION_AGG(document_hll) AS metric_value "+
		"FROM sum_document_hll_daily WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31' GROUP BY dt")
	assert.Contains(t, got, "ORDER BY event_date ASC")
	assert.NotContains(t, got, "UNION ALL")
	assert.NotContains(t, got, "document_events")
}

func TestSeriesQueryEntirelyTodayBaseOnly(t *testing.T) {
	got := norm(descUser.seriesQuery(types.ID("p1"), day("2026-08-31"), day("2026-09-01"), day("2026-08-31")))

	assert.Contains(t, got, "APPROX_COUNT_DISTINCT(user_id) AS metric_value FROM user_events")
	assert.NotContains(t, got, "sum_user_hll_daily")
	assert.NotContains(t, got, "UNION ALL")
}

func TestTotalQueryClientCarriesEventType(t *testing.T) {
	got := norm(descClient.totalQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), day("2026-08-31")))

	// summary half keyed by event_type
	assert.Contains(t, got, "SELECT client_hll AS sketch FROM sum_client_hll_daily "+
		"WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31' AND event_type = 'client-activated'")
	// fresh half filters event_type too
	assert.Contains(t, got, "HLL_UNION(HLL_HASH(client_id)) AS sketch FROM client_events")
	assert.Contains(t, got, "AND event_type = 'client-activated'")
}

func TestPeakTotalQueryIsMaxNoBoundaryUnion(t *testing.T) {
	got := norm(peakTotalQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), day("2026-08-31")))

	assert.Contains(t, got, "SELECT MAX(session_count) FROM")
	assert.Contains(t, got, "HLL_UNION_AGG(session_hll) AS session_count "+
		"FROM sum_session_hll_daily_ch WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31' "+
		"GROUP BY dt, channel_key")
	assert.Contains(t, got, "APPROX_COUNT_DISTINCT(session_id) AS session_count FROM session_events")
	assert.Contains(t, got, "GROUP BY DATE(timestamp), channel_key")
	// peak never unions sketches across the today boundary
	assert.NotContains(t, got, "HLL_UNION_AGG(sketch)")
}

func TestPeakSeriesQueryStraddling(t *testing.T) {
	got := norm(peakSeriesQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), day("2026-08-31")))

	assert.Contains(t, got, "SELECT event_date, metric_value FROM")
	assert.Contains(t, got, "MAX(session_count) AS metric_value")
	assert.Contains(t, got, "sum_session_hll_daily_ch")
	assert.Contains(t, got, "UNION ALL")
	assert.Contains(t, got, "session_events")
	assert.Contains(t, got, "ORDER BY event_date ASC")
}

func TestSeriesQueryStraddlingConcatenatesHalves(t *testing.T) {
	got := norm(descUser.seriesQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), day("2026-08-31")))

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
	got := norm(descSession.totalQuery(types.ID("p1"), day("2026-08-01"), day("2026-09-01"), day("2026-08-31")))

	// distinct sessions across the whole window: union every channel-day sketch,
	// cardinality once. The summary half must NOT filter or group by channel_key.
	assert.Contains(t, got, "SELECT HLL_UNION_AGG(sketch) FROM")
	assert.Contains(t, got, "SELECT session_hll AS sketch FROM sum_session_hll_daily_ch "+
		"WHERE project_id = 'p1' AND dt >= '2026-08-01' AND dt < '2026-08-31'")
	assert.Contains(t, got, "SELECT HLL_UNION(HLL_HASH(session_id)) AS sketch FROM session_events")
	assert.NotContains(t, got, "channel_key")
}

func TestTotalQueryEmptyWindowNoUnion(t *testing.T) {
	got := norm(descUser.totalQuery(types.ID("p1"), day("2026-08-31"), day("2026-08-31"), day("2026-08-31")))

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
	from, to, today := day("2026-08-01"), day("2026-09-01"), day("2026-08-31")
	queries := map[string]string{
		"series":      descUser.seriesQuery(types.ID("p1"), from, to, today),
		"total":       descUser.totalQuery(types.ID("p1"), from, to, today),
		"peak series": peakSeriesQuery(types.ID("p1"), from, to, today),
		"peak total":  peakTotalQuery(types.ID("p1"), from, to, today),
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

// The total's fresh half must aggregate per day into one sketch. A per-row
// HLL_HASH(col) inside the UNION ALL branch leaves the HLL_UNION_AGG above the
// union, which StarRocks cannot push into the rollup, so the branch full-scans
// the base even with a DATE-only predicate. HLL_UNION(HLL_HASH(col)) GROUP BY
// DATE(timestamp) matches the MV's own shape and rewrites; the outer
// HLL_UNION_AGG then merges the two halves, unchanged.
func TestTotalQueryFreshHalfPreAggregatesPerDay(t *testing.T) {
	from, to, today := day("2026-08-01"), day("2026-09-01"), day("2026-08-31")
	for name, d := range map[string]metricDesc{
		"user":    descUser,
		"client":  descClient,
		"session": descSession,
	} {
		t.Run(name, func(t *testing.T) {
			got := norm(d.totalQuery(types.ID("p1"), from, to, today))

			assert.Contains(t, got, "SELECT HLL_UNION(HLL_HASH("+d.idColumn+")) AS sketch")
			assert.Contains(t, got, "GROUP BY DATE(timestamp)")
			assert.NotContains(t, got, "SELECT HLL_HASH("+d.idColumn+") AS sketch")
			// cardinality is still taken exactly once, above the union
			assert.Contains(t, got, "SELECT HLL_UNION_AGG(sketch) FROM")
		})
	}
}

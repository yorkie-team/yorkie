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
	"strings"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
)

// The dual-read query builders split the requested window at the UTC day today:
// the historical part [from, today) is served by the decoupled daily HLL
// summary tables, and the fresh part [today, to) by the base rollups. Totals
// union the two halves' sketches and count once with HLL_UNION_AGG, so a
// subject active in both halves counts once. Note HLL_UNION_AGG already returns
// the merged cardinality (a bigint), so it is not wrapped in HLL_CARDINALITY.
// These builders run only when SummaryEnabled is true; the flag-off path keeps
// the base-only queries in starrocks.go unchanged.
// See docs/design/project-stats-long-retention.md.

// dayFmt formats a time as the StarRocks date literal used throughout.
func dayFmt(t time.Time) string {
	return t.Format("2006-01-02")
}

// summaryEventTypePred returns the summary-table event_type predicate for the
// client metric, or an empty string for the others.
func (d metricDesc) summaryEventTypePred() string {
	if d.eventType == "" {
		return ""
	}
	return fmt.Sprintf(" AND event_type = '%s'", d.eventType)
}

// basePred returns the fresh-half base-table predicate: raw timestamp bounds so
// a partitioned base prunes to today, plus DATE(timestamp) bounds so the MV
// rewrite still matches, plus the optional event_type filter.
func (d metricDesc) basePred(id types.ID, fresh dayRange) string {
	start, end := dayFmt(fresh.Start), dayFmt(fresh.End)
	pred := fmt.Sprintf(
		"project_id = '%s' AND timestamp >= '%s' AND timestamp < '%s' "+
			"AND DATE(timestamp) >= '%s' AND DATE(timestamp) < '%s'",
		id.String(), start, end, start, end,
	)
	if d.eventType != "" {
		pred += fmt.Sprintf(" AND event_type = '%s'", d.eventType)
	}
	return pred
}

// join wraps the non-empty parts in UNION ALL. When every part is empty it
// returns the first part, whose empty range yields no rows.
func join(parts []string) string {
	nonEmpty := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) == 0 {
		return parts[0]
	}
	return strings.Join(nonEmpty, "\nUNION ALL\n")
}

// seriesQuery builds the per-day series for a simple distinct-count metric
// (users, documents, channels, sessions) as a dual read.
func (d metricDesc) seriesQuery(id types.ID, from, to, today time.Time) string {
	hist, fresh := splitWindow(from, to, today)

	var histSQL, freshSQL string
	if !hist.Empty || fresh.Empty {
		histSQL = fmt.Sprintf(
			"SELECT dt AS event_date, HLL_UNION_AGG(%s) AS metric_value "+
				"FROM %s WHERE project_id = '%s' AND dt >= '%s' AND dt < '%s'%s GROUP BY dt",
			d.hllColumn, d.summaryTable, id.String(),
			dayFmt(from), dayFmt(hist.End), d.summaryEventTypePred(),
		)
	}
	if !fresh.Empty {
		freshSQL = fmt.Sprintf(
			"SELECT DATE(timestamp) AS event_date, APPROX_COUNT_DISTINCT(%s) AS metric_value "+
				"FROM %s WHERE %s GROUP BY DATE(timestamp)",
			d.idColumn, d.baseTable, d.basePred(id, fresh),
		)
	}

	//nolint:gosec
	return fmt.Sprintf(
		"SELECT event_date, metric_value FROM (\n%s\n) t ORDER BY event_date ASC;",
		join([]string{histSQL, freshSQL}),
	)
}

// totalQuery builds the whole-window distinct total as a dual read, unioning
// the summary sketches with the fresh half's per-row sketches and taking
// cardinality exactly once.
func (d metricDesc) totalQuery(id types.ID, from, to, today time.Time) string {
	hist, fresh := splitWindow(from, to, today)

	var histSQL, freshSQL string
	if !hist.Empty || fresh.Empty {
		histSQL = fmt.Sprintf(
			"SELECT %s AS sketch FROM %s WHERE project_id = '%s' AND dt >= '%s' AND dt < '%s'%s",
			d.hllColumn, d.summaryTable, id.String(),
			dayFmt(from), dayFmt(hist.End), d.summaryEventTypePred(),
		)
	}
	if !fresh.Empty {
		freshSQL = fmt.Sprintf(
			"SELECT HLL_HASH(%s) AS sketch FROM %s WHERE %s",
			d.idColumn, d.baseTable, d.basePred(id, fresh),
		)
	}

	//nolint:gosec
	return fmt.Sprintf(
		"SELECT HLL_UNION_AGG(sketch) FROM (\n%s\n) t;",
		join([]string{histSQL, freshSQL}),
	)
}

// peakSeriesQuery builds the per-day peak-sessions-per-channel series as a dual
// read: the daily peak is MAX over channels of the per-channel distinct
// sessions, which needs no cross-boundary union because each day is independent.
func peakSeriesQuery(id types.ID, from, to, today time.Time) string {
	hist, fresh := splitWindow(from, to, today)
	d := descSession

	var histSQL, freshSQL string
	if !hist.Empty || fresh.Empty {
		histSQL = fmt.Sprintf(
			"SELECT event_date, MAX(session_count) AS metric_value FROM ("+
				"SELECT dt AS event_date, channel_key, HLL_UNION_AGG(session_hll) AS session_count "+
				"FROM %s WHERE project_id = '%s' AND dt >= '%s' AND dt < '%s' GROUP BY dt, channel_key"+
				") hc GROUP BY event_date",
			d.summaryTable, id.String(), dayFmt(from), dayFmt(hist.End),
		)
	}
	if !fresh.Empty {
		freshSQL = fmt.Sprintf(
			"SELECT event_date, MAX(session_count) AS metric_value FROM ("+
				"SELECT DATE(timestamp) AS event_date, channel_key, APPROX_COUNT_DISTINCT(session_id) AS session_count "+
				"FROM %s WHERE %s GROUP BY DATE(timestamp), channel_key"+
				") fc GROUP BY event_date",
			d.baseTable, d.basePred(id, fresh),
		)
	}

	//nolint:gosec
	return fmt.Sprintf(
		"SELECT event_date, metric_value FROM (\n%s\n) t ORDER BY event_date ASC;",
		join([]string{histSQL, freshSQL}),
	)
}

// peakTotalQuery builds the whole-window peak sessions per channel: the single
// highest per-(day, channel) distinct-session count. It is a MAX over
// independent buckets, so no cross-boundary sketch union is needed.
func peakTotalQuery(id types.ID, from, to, today time.Time) string {
	hist, fresh := splitWindow(from, to, today)
	d := descSession

	var histSQL, freshSQL string
	if !hist.Empty || fresh.Empty {
		histSQL = fmt.Sprintf(
			"SELECT HLL_UNION_AGG(session_hll) AS session_count "+
				"FROM %s WHERE project_id = '%s' AND dt >= '%s' AND dt < '%s' GROUP BY dt, channel_key",
			d.summaryTable, id.String(), dayFmt(from), dayFmt(hist.End),
		)
	}
	if !fresh.Empty {
		freshSQL = fmt.Sprintf(
			"SELECT APPROX_COUNT_DISTINCT(session_id) AS session_count "+
				"FROM %s WHERE %s GROUP BY DATE(timestamp), channel_key",
			d.baseTable, d.basePred(id, fresh),
		)
	}

	//nolint:gosec
	return fmt.Sprintf(
		"SELECT MAX(session_count) FROM (\n%s\n) t;",
		join([]string{histSQL, freshSQL}),
	)
}

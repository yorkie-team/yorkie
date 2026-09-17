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

// The dual-read query builders split the requested window against a
// caller-supplied coverage range: the part of the window inside it is served by
// the decoupled daily summary tables, and the parts outside it — the days below
// the summary's first row and the days above its last — by the base rollups.
// The coverage range is the summary's own [MIN(dt), MAX(dt) + 1), not a window
// ending at today, see coverage.go. The two base branches a window can produce
// differ only in their date bounds, so each builder emits them from one helper.
// Every summary but peak's stores an HLL sketch, because a distinct count is
// only mergeable across days through a sketch; peak's stores a plain integer,
// because a MAX is mergeable as it is.
// Totals union every range's sketches and count once with HLL_UNION_AGG, so a
// subject active in more than one of them counts once. Note HLL_UNION_AGG
// already returns the merged cardinality (a bigint), so it is not wrapped in
// HLL_CARDINALITY.
// These builders run only when SummaryEnabled is true; the flag-off path keeps
// the base-only queries in starrocks.go unchanged.
// See docs/design/project-stats-long-retention.md.

// dayFmt formats a time as the StarRocks date literal used throughout. It
// normalizes to UTC first so the summary-path literals line up with the UTC day
// the split lands on, even when from/to arrive in the server's local zone.
func dayFmt(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// summaryEventTypePred returns the summary-table event_type predicate for the
// client metric, or an empty string for the others.
func (d metricDesc) summaryEventTypePred() string {
	if d.eventType == "" {
		return ""
	}
	return fmt.Sprintf(" AND event_type = '%s'", d.eventType)
}

// basePred returns a base-half base-table predicate: DATE(timestamp) bounds
// plus the optional event_type filter.
//
// The bounds must go through DATE(timestamp) and nothing else. The sync MV
// mv_*_hll_daily carries only mv_dt = DATE(timestamp), not the raw timestamp
// column, so a query that mentions raw timestamp drops out of the rollup's
// candidate set and full-scans the base event table instead. A raw bound does
// prune partitions on a date-partitioned base, but that is a false economy: the
// MV holds one row per (project, day), so scanning all of its partitions is far
// cheaper than one raw partition of the base.
func (d metricDesc) basePred(id types.ID, days dayRange) string {
	pred := fmt.Sprintf(
		"project_id = '%s' AND DATE(timestamp) >= '%s' AND DATE(timestamp) < '%s'",
		id.String(), dayFmt(days.Start), dayFmt(days.End),
	)
	if d.eventType != "" {
		pred += fmt.Sprintf(" AND event_type = '%s'", d.eventType)
	}
	return pred
}

// join wraps the non-empty parts in UNION ALL. Every caller emits the summary
// half whenever both base halves are empty, so at least one part is always
// non-empty; the all-empty fallback returns parts[0] only to keep the function
// total.
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

// emitSummary reports whether the summary branch belongs in the query: whenever
// it has days to serve, and also when no range has any, so a degenerate window
// still produces one syntactically complete branch rather than no query at all.
// A window that reaches back before the summary but not into it emits the base
// branches alone — asking the summary for a range it holds nothing in would add
// a scan that can only return nothing.
func emitSummary(pre, hist, fresh dayRange) bool {
	return !hist.Empty || (pre.Empty && fresh.Empty)
}

// baseSeries is one per-day series branch read from the base rollup, used for
// both the days below the summary's coverage and the days above it.
func (d metricDesc) baseSeries(id types.ID, days dayRange) string {
	return fmt.Sprintf(
		"SELECT DATE(timestamp) AS event_date, APPROX_COUNT_DISTINCT(%s) AS metric_value "+
			"FROM %s WHERE %s GROUP BY DATE(timestamp)",
		d.idColumn, d.baseTable, d.basePred(id, days),
	)
}

// seriesQuery builds the per-day series for a simple distinct-count metric
// (users, documents, channels, sessions) as a dual read.
func (d metricDesc) seriesQuery(id types.ID, from, to time.Time, cov dayRange) string {
	pre, hist, fresh := splitWindow(from, to, cov)

	var preSQL, histSQL, freshSQL string
	if !pre.Empty {
		preSQL = d.baseSeries(id, pre)
	}
	if emitSummary(pre, hist, fresh) {
		histSQL = fmt.Sprintf(
			"SELECT dt AS event_date, HLL_UNION_AGG(%s) AS metric_value "+
				"FROM %s WHERE project_id = '%s' AND dt >= '%s' AND dt < '%s'%s GROUP BY dt",
			d.hllColumn, d.summaryTable, id.String(),
			dayFmt(hist.Start), dayFmt(hist.End), d.summaryEventTypePred(),
		)
	}
	if !fresh.Empty {
		freshSQL = d.baseSeries(id, fresh)
	}

	//nolint:gosec
	return fmt.Sprintf(
		"SELECT event_date, metric_value FROM (\n%s\n) t ORDER BY event_date ASC;",
		join([]string{preSQL, histSQL, freshSQL}),
	)
}

// baseSketch is one daily-sketch branch read from the base rollup, used for both
// the days below the summary's coverage and the days above it.
//
// It aggregates per day rather than emitting one HLL_HASH(id) per row: with a
// per-row projection the outer HLL_UNION_AGG sits above the UNION ALL, which
// StarRocks cannot push into the rollup, so the branch full-scans the base even
// with a DATE-only predicate. HLL_UNION(HLL_HASH(id)) GROUP BY DATE(timestamp)
// is the MV's own shape and rewrites onto it. HLL union is associative, so
// merging one sketch per day instead of one per row is the same distinct count.
func (d metricDesc) baseSketch(id types.ID, days dayRange) string {
	return fmt.Sprintf(
		"SELECT HLL_UNION(HLL_HASH(%s)) AS sketch FROM %s WHERE %s GROUP BY DATE(timestamp)",
		d.idColumn, d.baseTable, d.basePred(id, days),
	)
}

// totalQuery builds the whole-window distinct total as a dual read, unioning
// the summary's daily sketches with the base halves' and taking cardinality
// exactly once.
func (d metricDesc) totalQuery(id types.ID, from, to time.Time, cov dayRange) string {
	pre, hist, fresh := splitWindow(from, to, cov)

	var preSQL, histSQL, freshSQL string
	if !pre.Empty {
		preSQL = d.baseSketch(id, pre)
	}
	if emitSummary(pre, hist, fresh) {
		histSQL = fmt.Sprintf(
			"SELECT %s AS sketch FROM %s WHERE project_id = '%s' AND dt >= '%s' AND dt < '%s'%s",
			d.hllColumn, d.summaryTable, id.String(),
			dayFmt(hist.Start), dayFmt(hist.End), d.summaryEventTypePred(),
		)
	}
	if !fresh.Empty {
		freshSQL = d.baseSketch(id, fresh)
	}

	//nolint:gosec
	return fmt.Sprintf(
		"SELECT HLL_UNION_AGG(sketch) FROM (\n%s\n) t;",
		join([]string{preSQL, histSQL, freshSQL}),
	)
}

// basePeak is one per-day peak branch read from the base rollup, used for both
// the days below the summary's coverage and the days above it. Neither has a
// precomputed row to read, so each reduces per (day, channel) and takes the
// daily MAX in-branch.
//
// channel_key stays literal where idColumn does not: it is not a property of
// the metric but the grain peak is defined over, so this builder only fits a
// base table that carries it.
func (d metricDesc) basePeak(id types.ID, days dayRange) string {
	return fmt.Sprintf(
		"SELECT event_date, MAX(session_count) AS metric_value FROM ("+
			"SELECT DATE(timestamp) AS event_date, channel_key, APPROX_COUNT_DISTINCT(%s) AS session_count "+
			"FROM %s WHERE %s GROUP BY DATE(timestamp), channel_key"+
			") fc GROUP BY event_date",
		d.idColumn, d.baseTable, d.basePred(id, days),
	)
}

// peakSeriesQuery builds the per-day peak-sessions-per-channel series as a dual
// read. The daily peak is a MAX over independent per-(day, channel) buckets, so
// each day stands alone and no sketch is unioned across a split boundary.
//
// The summary half and the base halves have different shapes on purpose. The
// summary half reads summaryTable, which already holds the finished peak as one
// plain integer per (project, day): the refresh job did the MAX over channels
// when it wrote the row, so the read costs days rather than channels x days.
// The MAX ... GROUP BY dt is kept rather than reading the column bare so the
// result does not depend on the aggregate table merging its duplicate keys at
// read time. A base half has no precomputed row to read, so it still does the
// per-(day, channel) APPROX_COUNT_DISTINCT itself.
//
// It is a method so the metric it reads and the coverage the caller splits on
// cannot name different tables.
func (d metricDesc) peakSeriesQuery(id types.ID, from, to time.Time, cov dayRange) string {
	pre, hist, fresh := splitWindow(from, to, cov)

	var preSQL, histSQL, freshSQL string
	if !pre.Empty {
		preSQL = d.basePeak(id, pre)
	}
	if emitSummary(pre, hist, fresh) {
		histSQL = fmt.Sprintf(
			"SELECT dt AS event_date, MAX(%s) AS metric_value "+
				"FROM %s WHERE project_id = '%s' AND dt >= '%s' AND dt < '%s' GROUP BY dt",
			d.peakColumn, d.summaryTable, id.String(), dayFmt(hist.Start), dayFmt(hist.End),
		)
	}
	if !fresh.Empty {
		freshSQL = d.basePeak(id, fresh)
	}

	//nolint:gosec
	return fmt.Sprintf(
		"SELECT event_date, metric_value FROM (\n%s\n) t ORDER BY event_date ASC;",
		join([]string{preSQL, histSQL, freshSQL}),
	)
}

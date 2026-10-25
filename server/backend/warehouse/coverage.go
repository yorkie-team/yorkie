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
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/yorkie-team/yorkie/server/logging"
)

// The dual read may only serve a day from the summary once the refresh job has
// written it, so it splits the window at the summary's own coverage rather than
// at a fixed today, which would leave the days the job has not reached yet in
// neither half. Coverage is a range, [MIN(dt), MAX(dt) + 1), not a watermark:
// the days below the summary's first row are as absent from it as the days
// above its last, and both come from the base.
// See docs/design/project-stats-long-retention.md.

// coverageTTL is how long a probe is reused. It is what collapses one dashboard
// load onto a single probe: days holds its lock across the fetch, so the
// eleven metric queries GetProjectStats fans out arrive one at a time and all
// but the first find a result younger than the TTL. A minute also covers
// consecutive loads, and costs nothing in staleness — the values it reads move
// at most once per refresh run.
const coverageTTL = time.Minute

// summaryDays is the first and last day one summary table holds. The zero value
// means the table holds nothing, which is what an empty or unprobed table
// reports.
type summaryDays struct {
	Min time.Time
	Max time.Time
}

// coverageQuery reads the first and last day present in every summary table in
// a single round trip.
func coverageQuery() string {
	parts := make([]string, 0, len(allDescs))
	for _, d := range allDescs {
		parts = append(parts, fmt.Sprintf(
			"SELECT '%s' AS summary_table, MIN(dt) AS min_dt, MAX(dt) AS max_dt FROM %s",
			d.summaryTable, d.summaryTable,
		))
	}
	return strings.Join(parts, "\nUNION ALL\n") + ";"
}

// coverageDays returns the half-open day range the summary can serve: from the
// first day it holds to the first day it does not.
//
// The end is MAX(dt) + 1, never past today. A backfill that included the
// running day leaves a partial sketch in the summary, and serving that would
// undercount today.
//
// The start is MIN(dt), and it is what keeps MAX(dt) from being trusted as a
// coverage set. A window reaching back before the summary's first row would
// otherwise be served from a summary that has no row for those days, which
// reads as zero; the caller routes them to the base instead. What this does not
// see is a hole inside the range: the probe is global rather than per project,
// so a day missing for one project only is still below MAX(dt) and still reads
// as zero.
//
// An Empty range means the summary covers nothing and the whole window comes
// from the base. That is the answer for an empty or unprobed table, and for a
// summary holding nothing but the running day, whose only day the clamp takes
// away.
func coverageDays(days summaryDays, today time.Time) dayRange {
	if days.Min.IsZero() || days.Max.IsZero() {
		return dayRange{Empty: true}
	}

	boundary := days.Max.AddDate(0, 0, 1)
	if boundary.After(today) {
		boundary = today
	}
	return newDayRange(days.Min, boundary)
}

// coverageProbe is one probe's answer: the days each summary table holds, and
// the rollups the reads name that the hinted read does not resolve yet. A table
// the probe did not report holds the zero value, i.e. covers nothing.
type coverageProbe struct {
	days       map[string]summaryDays
	unresolved map[string]bool
}

// coverageCache holds the last coverage probe. The zero value is usable and
// probes on first use.
type coverageCache struct {
	mu         sync.Mutex
	fetchedAt  time.Time
	probed     map[string]summaryDays
	unresolved map[string]bool
}

// probe returns the cached probe, running fetch when it is missing or stale.
func (c *coverageCache) probe(
	ctx context.Context,
	fetch func(context.Context) (coverageProbe, error),
) (coverageProbe, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.probed == nil || time.Since(c.fetchedAt) > coverageTTL {
		p, err := fetch(ctx)
		if err != nil {
			return coverageProbe{}, err
		}
		c.probed, c.unresolved, c.fetchedAt = p.days, p.unresolved, time.Now()
	}

	return coverageProbe{days: c.probed, unresolved: c.unresolved}, nil
}

// fetchCoverage runs the coverage probe against StarRocks: the coverage query,
// then one hinted read per rollup the reads name.
func (r *StarRocks) fetchCoverage(ctx context.Context) (coverageProbe, error) {
	days, err := r.fetchSummaryDays(ctx)
	if err != nil {
		return coverageProbe{}, err
	}

	unresolved := make(map[string]bool)
	for _, d := range allDescs {
		if d.summaryRollup == "" {
			continue
		}
		ok, err := r.rollupResolves(ctx, d)
		if err != nil {
			return coverageProbe{}, err
		}
		if !ok {
			unresolved[d.summaryRollup] = true
		}
	}

	return coverageProbe{days: days, unresolved: unresolved}, nil
}

// rollupResolves reports whether d's rollup can be read by name. A cluster
// whose summary table predates the rollup, or whose build has not reached
// FINISHED, answers the hinted read with Unknown table: that is a no, and the
// reads fall back to the summary table until a later probe says yes. Any other
// error, from StarRocks or on the way to it, is not an answer about the rollup
// and fails the probe like the coverage query: a timeout or a restarting BE
// must not move every sessions read onto the heavier plan for a TTL.
func (r *StarRocks) rollupResolves(ctx context.Context, d metricDesc) (bool, error) {
	// The name comes from a descriptor, never from a request.
	//nolint:gosec
	rows, err := r.driver.QueryContext(ctx, "SELECT 1 FROM "+d.summarySource()+" LIMIT 1")
	if err != nil {
		if isUnknownTable(err) {
			logging.DefaultLogger().Warnf(
				"read %s instead of %s, which is not queryable yet: %v", d.summaryTable, d.summaryRollup, err,
			)
			return false, nil
		}
		return false, fmt.Errorf("probe rollup %s: %w", d.summaryRollup, err)
	}
	closeRows(rows)
	return true, nil
}

// isUnknownTable reports whether StarRocks rejected a query for naming a table
// or index it does not have. StarRocks reports every analysis error as 5502, so
// the message is what tells this one apart.
func isUnknownTable(err error) bool {
	var serverErr *mysql.MySQLError
	return errors.As(err, &serverErr) && strings.Contains(serverErr.Message, "Unknown table")
}

// fetchSummaryDays runs the coverage query.
func (r *StarRocks) fetchSummaryDays(ctx context.Context) (map[string]summaryDays, error) {
	rows, err := r.driver.QueryContext(ctx, coverageQuery())
	if err != nil {
		return nil, fmt.Errorf("query summary coverage: %w", err)
	}
	defer closeRows(rows)

	probed := make(map[string]summaryDays, len(allDescs))
	for rows.Next() {
		var table string
		var minDt, maxDt sql.NullString
		if err := rows.Scan(&table, &minDt, &maxDt); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		// An empty table reports both as NULL, which is the zero value: it
		// covers nothing, so it is left out of the map entirely.
		if !minDt.Valid || !maxDt.Valid {
			continue
		}

		first, err := time.Parse("2006-01-02", minDt.String)
		if err != nil {
			return nil, fmt.Errorf("parse min dt: %w", err)
		}
		last, err := time.Parse("2006-01-02", maxDt.String)
		if err != nil {
			return nil, fmt.Errorf("parse max dt: %w", err)
		}
		probed[table] = summaryDays{Min: first, Max: last}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return probed, nil
}

// summaryCoverage returns the day range the given metric's summary can serve,
// the range its dual read splits the window against, and the metric as the
// read should use it: without its rollup when the probe found the rollup does
// not resolve.
func (r *StarRocks) summaryCoverage(ctx context.Context, d metricDesc) (metricDesc, dayRange, error) {
	p, err := r.coverage.probe(ctx, r.fetchCoverage)
	if err != nil {
		return d, dayRange{Empty: true}, err
	}

	if p.unresolved[d.summaryRollup] {
		d.summaryRollup = ""
	}
	return d, coverageDays(p.days[d.summaryTable], todayUTC()), nil
}

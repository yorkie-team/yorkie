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
	"strings"
	"sync"
	"time"
)

// The dual read may only serve a day from the summary once the refresh job has
// written it, so it splits the window at the summary's own coverage rather than
// at a fixed today, which would leave the days the job has not reached yet in
// neither half. See docs/design/project-stats-long-retention.md.

// coverageTTL is how long a probe is reused. It is what collapses one dashboard
// load onto a single probe: maxDay holds its lock across the fetch, so the
// twelve metric queries GetProjectStats fans out arrive one at a time and all
// but the first find a result younger than the TTL. A minute also covers
// consecutive loads, and costs nothing in staleness — the value it reads moves
// at most once per refresh run.
const coverageTTL = time.Minute

// coverageQuery reads the last day present in every summary table in a single
// round trip.
func coverageQuery() string {
	parts := make([]string, 0, len(allDescs))
	for _, d := range allDescs {
		parts = append(parts, fmt.Sprintf(
			"SELECT '%s' AS summary_table, MAX(dt) AS max_dt FROM %s",
			d.summaryTable, d.summaryTable,
		))
	}
	return strings.Join(parts, "\nUNION ALL\n") + ";"
}

// coverageBoundary returns the first day the summary does not cover, the day the
// dual read switches from the summary to the base. It never passes today: a
// backfill that included the running day leaves a partial sketch in the summary,
// and serving that would undercount today.
func coverageBoundary(maxDt, today, from time.Time) time.Time {
	if maxDt.IsZero() {
		// The summary holds nothing for this metric, so it covers nothing and
		// the whole window is read from the base.
		return from
	}

	boundary := maxDt.AddDate(0, 0, 1)
	if boundary.After(today) {
		return today
	}
	return boundary
}

// coverageCache holds the last coverage probe. The zero value is usable and
// probes on first use.
type coverageCache struct {
	mu        sync.Mutex
	fetchedAt time.Time
	maxDays   map[string]time.Time
}

// maxDay returns the last day the given summary table holds, running fetch when
// the cached probe is missing or stale. A table the probe did not report is
// reported as the zero day, i.e. covering nothing.
func (c *coverageCache) maxDay(
	ctx context.Context,
	table string,
	fetch func(context.Context) (map[string]time.Time, error),
) (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.maxDays == nil || time.Since(c.fetchedAt) > coverageTTL {
		maxDays, err := fetch(ctx)
		if err != nil {
			return time.Time{}, err
		}
		c.maxDays, c.fetchedAt = maxDays, time.Now()
	}

	return c.maxDays[table], nil
}

// fetchCoverage runs the coverage probe against StarRocks.
func (r *StarRocks) fetchCoverage(ctx context.Context) (map[string]time.Time, error) {
	rows, err := r.driver.QueryContext(ctx, coverageQuery())
	if err != nil {
		return nil, fmt.Errorf("query summary coverage: %w", err)
	}
	defer closeRows(rows)

	maxDays := make(map[string]time.Time, len(allDescs))
	for rows.Next() {
		var table string
		var maxDt sql.NullString
		if err := rows.Scan(&table, &maxDt); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		if !maxDt.Valid {
			continue
		}

		day, err := time.Parse("2006-01-02", maxDt.String)
		if err != nil {
			return nil, fmt.Errorf("parse max dt: %w", err)
		}
		maxDays[table] = day
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return maxDays, nil
}

// splitDay returns the day the given metric's dual read splits the window on:
// the summary's coverage boundary, clamped to today.
func (r *StarRocks) splitDay(ctx context.Context, d metricDesc, from time.Time) (time.Time, error) {
	maxDt, err := r.coverage.maxDay(ctx, d.summaryTable, r.fetchCoverage)
	if err != nil {
		return time.Time{}, err
	}

	return coverageBoundary(maxDt, todayUTC(), from), nil
}

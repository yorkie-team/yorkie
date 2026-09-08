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

	"github.com/yorkie-team/yorkie/server/logging"
)

// The dual read may only serve a day from the summary once the refresh job has
// written it. Splitting at a fixed today assumes the summary is complete
// through yesterday, but the job runs on a schedule: between the moment a day
// completes and the next run, that day is in neither half — not in the summary,
// and not in the base half that starts at today — so it reads as zero and drags
// the window's total down with it. Splitting at the summary's own coverage
// instead closes the gap whatever the cadence, and survives a missed run.
// See docs/design/project-stats-long-retention.md.

// coverageTTL is how long a coverage probe is reused. The value it reads moves
// at most once per refresh run, so a short window is enough to answer a whole
// dashboard load from one probe while still picking up a fresh run promptly.
const coverageTTL = time.Minute

// coverageQuery reads the last day present in every summary table in a single
// round trip. GetProjectStats fans out twelve metric queries at once, and
// caching one probe for all of them keeps that from becoming twelve more.
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
		// the whole window comes from the base — the flag-off numbers.
		return from
	}

	boundary := maxDt.AddDate(0, 0, 1)
	if boundary.After(today) {
		return today
	}
	return boundary
}

// coverageCache holds the last coverage probe. The zero value is usable: it
// probes on first use and falls back to coverageTTL and time.Now.
type coverageCache struct {
	mu        sync.Mutex
	ttl       time.Duration
	now       func() time.Time
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

	now := time.Now
	if c.now != nil {
		now = c.now
	}
	ttl := c.ttl
	if ttl == 0 {
		ttl = coverageTTL
	}

	if c.maxDays == nil || now().Sub(c.fetchedAt) > ttl {
		maxDays, err := fetch(ctx)
		if err != nil {
			return time.Time{}, err
		}
		c.maxDays, c.fetchedAt = maxDays, now()
	}

	return c.maxDays[table], nil
}

// fetchCoverage runs the coverage probe against StarRocks.
func (r *StarRocks) fetchCoverage(ctx context.Context) (map[string]time.Time, error) {
	rows, err := r.driver.QueryContext(ctx, coverageQuery())
	if err != nil {
		return nil, fmt.Errorf("query summary coverage: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			logging.DefaultLogger().Errorf("close rows: %v", err)
		}
	}()

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

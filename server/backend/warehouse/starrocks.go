/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/server/logging"
)

// StarRocks is a warehouse that stores data in StarRocks.
//
// NOTE(hackerwins): The queries below filter on DATE(timestamp) instead of the
// raw timestamp. StarRocks rewrites an APPROX_COUNT_DISTINCT query onto a
// synchronous materialized view grouped by DATE(timestamp) only when the query
// uses the same expression in its predicate. With a raw timestamp predicate the
// query silently falls back to a full scan of the base event table.
type StarRocks struct {
	conf   *Config
	driver *sql.DB
}

// dial connects to the StarRocks.
func (r *StarRocks) dial(dsn string) error {
	driver, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open mysql driver: %w", err)
	}

	if err := driver.Ping(); err != nil {
		return fmt.Errorf("ping starrocks: %w", err)
	}

	r.driver = driver
	return nil
}

// summaryEnabled reports whether the dual-read summary path is turned on.
func (r *StarRocks) summaryEnabled() bool {
	return r.conf != nil && r.conf.SummaryEnabled
}

// todayUTC returns the start of the current day in UTC, the boundary the
// dual-read path splits the requested window on.
func todayUTC() time.Time {
	return time.Now().UTC().Truncate(24 * time.Hour)
}

// GetActiveUsers returns the number of active users in the given time range.
func (r *StarRocks) GetActiveUsers(
	ctx context.Context,
	id types.ID,
	from, to time.Time,
) ([]types.MetricPoint, error) {
	if err := validateTimeRange(from, to); err != nil {
		return nil, err
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT 
	    DATE(timestamp) AS event_date, 
	    APPROX_COUNT_DISTINCT(user_id) AS active_users
	FROM 
	    user_events
	WHERE
		project_id = '%s'
		AND DATE(timestamp) >= '%s'
		AND DATE(timestamp) < '%s'
	GROUP BY 
	    event_date
	ORDER BY 
	    event_date ASC;
	`, id.String(), from.Format("2006-01-02"), to.Format("2006-01-02"))

	if r.summaryEnabled() {
		query = descUser.seriesQuery(id, from, to, todayUTC())
	}

	metrics, err := r.queryMetrics(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get active users: %w", err)
	}
	return metrics, nil
}

// GetActiveUsersCount returns the total number of active users in the given time range.
func (r *StarRocks) GetActiveUsersCount(
	ctx context.Context,
	id types.ID,
	from, to time.Time,
) (int, error) {
	if err := validateTimeRange(from, to); err != nil {
		return 0, err
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT 
	    APPROX_COUNT_DISTINCT(user_id) AS active_users
	FROM 
	    user_events
	WHERE
		project_id = '%s'
		AND DATE(timestamp) >= '%s'
		AND DATE(timestamp) < '%s';
	`, id.String(), from.Format("2006-01-02"), to.Format("2006-01-02"))

	if r.summaryEnabled() {
		query = descUser.totalQuery(id, from, to, todayUTC())
	}

	count, err := r.queryCount(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("get active users count: %w", err)
	}
	return count, nil
}

// GetActiveDocuments returns the number of active documents in the given time range.
func (r *StarRocks) GetActiveDocuments(
	ctx context.Context,
	id types.ID,
	from, to time.Time,
) ([]types.MetricPoint, error) {
	if err := validateTimeRange(from, to); err != nil {
		return nil, err
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT 
	    DATE(timestamp) AS event_date, 
	    APPROX_COUNT_DISTINCT(document_key) AS active_documents
	FROM 
	    document_events
	WHERE
		project_id = '%s'
		AND DATE(timestamp) >= '%s'
		AND DATE(timestamp) < '%s'
	GROUP BY 
	    event_date
	ORDER BY 
	    event_date ASC;
	`, id.String(), from.Format("2006-01-02"), to.Format("2006-01-02"))

	if r.summaryEnabled() {
		query = descDocument.seriesQuery(id, from, to, todayUTC())
	}

	metrics, err := r.queryMetrics(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get active documents: %w", err)
	}
	return metrics, nil
}

// GetActiveDocumentsCount returns the total number of active documents in the given time range.
func (r *StarRocks) GetActiveDocumentsCount(
	ctx context.Context,
	id types.ID,
	from, to time.Time,
) (int, error) {
	if err := validateTimeRange(from, to); err != nil {
		return 0, err
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT 
		APPROX_COUNT_DISTINCT(document_key) AS active_documents
	FROM 
		document_events
	WHERE
		project_id = '%s'
		AND DATE(timestamp) >= '%s'
		AND DATE(timestamp) < '%s';
	`, id.String(), from.Format("2006-01-02"), to.Format("2006-01-02"))

	if r.summaryEnabled() {
		query = descDocument.totalQuery(id, from, to, todayUTC())
	}

	count, err := r.queryCount(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("get active documents count: %w", err)
	}
	return count, nil
}

// GetActiveClients returns the number of active clients in the given time range.
func (r *StarRocks) GetActiveClients(
	ctx context.Context,
	id types.ID,
	from, to time.Time,
) ([]types.MetricPoint, error) {
	if err := validateTimeRange(from, to); err != nil {
		return nil, err
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT
	    DATE(timestamp) AS event_date,
	    APPROX_COUNT_DISTINCT(client_id) AS active_clients
	FROM
	    client_events
	WHERE
		project_id = '%s'
		AND event_type = '%s'
		AND DATE(timestamp) >= '%s'
		AND DATE(timestamp) < '%s'
	GROUP BY
	    event_date
	ORDER BY
	    event_date ASC;
	`, id.String(), events.ClientActivatedEvent, from.Format("2006-01-02"), to.Format("2006-01-02"))

	if r.summaryEnabled() {
		query = descClient.seriesQuery(id, from, to, todayUTC())
	}

	metrics, err := r.queryMetrics(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get active clients: %w", err)
	}
	return metrics, nil
}

// GetActiveClientsCount returns the total number of active clients in the given time range.
func (r *StarRocks) GetActiveClientsCount(
	ctx context.Context,
	id types.ID,
	from, to time.Time,
) (int, error) {
	if err := validateTimeRange(from, to); err != nil {
		return 0, err
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT
		APPROX_COUNT_DISTINCT(client_id) AS active_clients
	FROM
		client_events
	WHERE
		project_id = '%s'
		AND event_type = '%s'
		AND DATE(timestamp) >= '%s'
		AND DATE(timestamp) < '%s';
	`, id.String(), events.ClientActivatedEvent, from.Format("2006-01-02"), to.Format("2006-01-02"))

	if r.summaryEnabled() {
		query = descClient.totalQuery(id, from, to, todayUTC())
	}

	count, err := r.queryCount(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("get active clients count: %w", err)
	}
	return count, nil
}

// GetActiveChannels returns the active channels of the given project.
func (r *StarRocks) GetActiveChannels(
	ctx context.Context,
	id types.ID,
	from, to time.Time,
) ([]types.MetricPoint, error) {
	if err := validateTimeRange(from, to); err != nil {
		return nil, err
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT 
	    DATE(timestamp) AS event_date, 
	    APPROX_COUNT_DISTINCT(channel_key) AS channels
	FROM 
	    channel_events
	WHERE
		project_id = '%s'
		AND DATE(timestamp) >= '%s'
		AND DATE(timestamp) < '%s'
	GROUP BY 
	    event_date
	ORDER BY 
	    event_date ASC;
	`, id.String(), from.Format("2006-01-02"), to.Format("2006-01-02"))

	if r.summaryEnabled() {
		query = descChannel.seriesQuery(id, from, to, todayUTC())
	}

	metrics, err := r.queryMetrics(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get active channels: %w", err)
	}
	return metrics, nil
}

// GetActiveChannelsCount returns the active channels count of the given project.
func (r *StarRocks) GetActiveChannelsCount(
	ctx context.Context,
	id types.ID,
	from, to time.Time,
) (int, error) {
	if err := validateTimeRange(from, to); err != nil {
		return 0, err
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT 
	    APPROX_COUNT_DISTINCT(channel_key) AS channels
	FROM 
	    channel_events
	WHERE
		project_id = '%s'
		AND DATE(timestamp) >= '%s'
		AND DATE(timestamp) < '%s';
	`, id.String(), from.Format("2006-01-02"), to.Format("2006-01-02"))

	if r.summaryEnabled() {
		query = descChannel.totalQuery(id, from, to, todayUTC())
	}

	count, err := r.queryCount(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("get active channels count: %w", err)
	}
	return count, nil
}

// GetSessions returns the sessions of the given project.
func (r *StarRocks) GetSessions(
	ctx context.Context,
	id types.ID,
	from, to time.Time,
) ([]types.MetricPoint, error) {
	if err := validateTimeRange(from, to); err != nil {
		return nil, err
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT 
	    DATE(timestamp) AS event_date, 
	    APPROX_COUNT_DISTINCT(session_id) AS sessions
	FROM 
	    session_events
	WHERE
		project_id = '%s'
		AND DATE(timestamp) >= '%s'
		AND DATE(timestamp) < '%s'
	GROUP BY 
	    event_date
	ORDER BY 
	    event_date ASC;
	`, id.String(), from.Format("2006-01-02"), to.Format("2006-01-02"))

	if r.summaryEnabled() {
		query = descSession.seriesQuery(id, from, to, todayUTC())
	}

	metrics, err := r.queryMetrics(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get sessions: %w", err)
	}
	return metrics, nil
}

// GetSessionsCount returns the sessions count of the given project.
func (r *StarRocks) GetSessionsCount(
	ctx context.Context,
	id types.ID,
	from, to time.Time,
) (int, error) {
	if err := validateTimeRange(from, to); err != nil {
		return 0, err
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT 
	    APPROX_COUNT_DISTINCT(session_id) AS sessions
	FROM 
	    session_events
	WHERE
		project_id = '%s'
		AND DATE(timestamp) >= '%s'
		AND DATE(timestamp) < '%s';
	`, id.String(), from.Format("2006-01-02"), to.Format("2006-01-02"))

	if r.summaryEnabled() {
		query = descSession.totalQuery(id, from, to, todayUTC())
	}

	count, err := r.queryCount(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("get sessions count: %w", err)
	}
	return count, nil
}

// GetPeakSessionsPerChannel returns the peak sessions per channel of the given project.
func (r *StarRocks) GetPeakSessionsPerChannel(
	ctx context.Context,
	id types.ID,
	from, to time.Time,
) ([]types.MetricPoint, error) {
	if err := validateTimeRange(from, to); err != nil {
		return nil, err
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT 
	    event_date,
	    MAX(session_count) AS peak_sessions
	FROM (
	    SELECT 
	        DATE(timestamp) AS event_date,
	        channel_key,
	        APPROX_COUNT_DISTINCT(session_id) AS session_count
	    FROM 
	        session_events
	    WHERE
	        project_id = '%s'
	        AND DATE(timestamp) >= '%s'
	        AND DATE(timestamp) < '%s'
	    GROUP BY 
	        event_date, channel_key
	) AS channel_sessions
	GROUP BY 
	    event_date
	ORDER BY 
	    event_date ASC;
	`, id.String(), from.Format("2006-01-02"), to.Format("2006-01-02"))

	if r.summaryEnabled() {
		query = peakSeriesQuery(id, from, to, todayUTC())
	}

	metrics, err := r.queryMetrics(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get peak sessions per channel: %w", err)
	}
	return metrics, nil
}

// GetPeakSessionsPerChannelCount returns the peak sessions per channel count of the given project.
func (r *StarRocks) GetPeakSessionsPerChannelCount(
	ctx context.Context,
	id types.ID,
	from, to time.Time,
) (int, error) {
	if err := validateTimeRange(from, to); err != nil {
		return 0, err
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT 
	    MAX(session_count) AS peak_sessions
	FROM (
	    SELECT 
	        DATE(timestamp) AS event_date,
	        channel_key,
	        APPROX_COUNT_DISTINCT(session_id) AS session_count
	    FROM 
	        session_events
	    WHERE
	        project_id = '%s'
	        AND DATE(timestamp) >= '%s'
	        AND DATE(timestamp) < '%s'
	    GROUP BY 
	        event_date, channel_key
	) AS channel_sessions;
	`, id.String(), from.Format("2006-01-02"), to.Format("2006-01-02"))

	if r.summaryEnabled() {
		query = peakTotalQuery(id, from, to, todayUTC())
	}

	count, err := r.queryCount(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("get peak sessions per channel count: %w", err)
	}
	return count, nil
}

// Close closes the connection to the StarRocks.
func (r *StarRocks) Close() error {
	if r.driver == nil {
		return nil
	}
	if err := r.driver.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}

	return nil
}

// validateTimeRange validates that the from time is before or equal to the to time.
func validateTimeRange(from, to time.Time) error {
	if from.After(to) {
		return fmt.Errorf("invalid time range")
	}
	return nil
}

// queryMetrics queries the metrics from the StarRocks.
func (r *StarRocks) queryMetrics(ctx context.Context, query string) ([]types.MetricPoint, error) {
	rows, err := r.driver.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			logging.DefaultLogger().Errorf("close rows: %v", err)
		}
	}()

	var metrics []types.MetricPoint
	for rows.Next() {
		var date string
		var val int32

		if err := rows.Scan(&date, &val); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		t, err := time.Parse("2006-01-02", date)
		if err != nil {
			return nil, fmt.Errorf("parse date: %w", err)
		}

		metrics = append(metrics, types.MetricPoint{
			Time:  t.Unix(),
			Value: int(val),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	return metrics, nil
}

// queryCount queries the count from the StarRocks.
func (r *StarRocks) queryCount(ctx context.Context, query string) (int, error) {
	rows, err := r.driver.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("query count: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			logging.DefaultLogger().Errorf("close rows: %v", err)
		}
	}()

	var count sql.NullInt64
	if rows.Next() {
		if err := rows.Scan(&count); err != nil {
			return 0, fmt.Errorf("scan row: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate rows: %w", err)
	}
	if !count.Valid {
		return 0, nil
	}
	return int(count.Int64), nil
}

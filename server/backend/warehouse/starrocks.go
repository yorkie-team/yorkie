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
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/logging"
)

// StarRocks is a warehouse that stores data in StarRocks.
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

// GetActiveUsers returns the number of active users in the given time range.
func (r *StarRocks) GetActiveUsers(id types.ID, from, to time.Time) ([]types.MetricPoint, error) {
	if from.After(to) {
		return nil, fmt.Errorf("invalid time range")
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT 
	    DATE(timestamp) AS event_date, 
	    COUNT(DISTINCT user_id) AS active_users
	FROM 
	    user_events
	WHERE
		project_id = '%s'
		AND timestamp >= '%s'
		AND timestamp <= '%s'
	GROUP BY 
	    event_date
	ORDER BY 
	    event_date ASC;
	`, id.String(), from.Format("2006-01-02"), to.Format("2006-01-02"))

	rows, err := r.driver.Query(query)
	if err != nil {
		return nil, fmt.Errorf("get active users: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			logging.DefaultLogger().Errorf("close rows: %v", err)
		}
	}()

	var activeUsers []types.MetricPoint
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

		activeUsers = append(activeUsers, types.MetricPoint{
			Time:  t.Unix(),
			Value: int(val),
		})
	}
	return activeUsers, nil
}

// GetActiveUsersCount returns the number of active users in the given time range.
func (r *StarRocks) GetActiveUsersCount(id types.ID, from, to time.Time) (int, error) {
	if from.After(to) {
		return 0, fmt.Errorf("invalid time range")
	}

	// NOTE(hackerwins): StarRocks supports MySQL Driver, but it does not support
	// Prepared Statement. So, we need to use string interpolation to build the query.
	//nolint:gosec
	query := fmt.Sprintf(`
	SELECT 
	    COUNT(DISTINCT user_id) AS active_users
	FROM 
	    user_events
	WHERE
		project_id = '%s'
		AND timestamp >= '%s'
		AND timestamp < '%s';
	`, id.String(), from.Format("2006-01-02"), to.Format("2006-01-02"))

	rows, err := r.driver.Query(query)
	if err != nil {
		return 0, fmt.Errorf("get active users count: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			logging.DefaultLogger().Errorf("close rows: %v", err)
		}
	}()

	var count int
	if rows.Next() {
		if err := rows.Scan(&count); err != nil {
			return 0, fmt.Errorf("scan row: %w", err)
		}
	}
	return count, nil
}

// Close closes the connection to the StarRocks.
func (r *StarRocks) Close() error {
	if err := r.driver.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}

	return nil
}

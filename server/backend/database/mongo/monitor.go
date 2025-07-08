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

package mongo

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/event"
	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/server/logging"
)

// QueryMonitor represents a MongoDB query monitor.
type QueryMonitor struct {
	logger logging.Logger
	config *MonitorConfig
}

// MonitorConfig represents configuration for MongoDB query monitoring.
type MonitorConfig struct {
	// Enabled determines whether query monitoring is enabled.
	Enabled bool

	// SlowQueryThreshold is the threshold in milliseconds to log slow queries.
	// If a query takes longer than this threshold, it will be logged as a slow query.
	SlowQueryThreshold time.Duration
}

// NewQueryMonitor creates a new instance of QueryMonitor.
func NewQueryMonitor(config *MonitorConfig) *QueryMonitor {
	return &QueryMonitor{
		logger: logging.New("mongo"),
		config: config,
	}
}

// CreateCommandMonitor creates a new instance of event.CommandMonitor
// which can be used to monitor MongoDB commands.
func (m *QueryMonitor) CreateCommandMonitor() *event.CommandMonitor {
	if !m.config.Enabled {
		return nil
	}

	return &event.CommandMonitor{
		Started: func(ctx context.Context, evt *event.CommandStartedEvent) {
			if logging.Enabled(zap.DebugLevel) {
				m.logger.Debugf("STAR: %d(%s): %s", evt.RequestID, evt.CommandName, evt.Command)
			}
		},
		Succeeded: func(ctx context.Context, evt *event.CommandSucceededEvent) {
			duration := evt.Duration.Milliseconds()

			if m.config.SlowQueryThreshold > 0 && evt.Duration > m.config.SlowQueryThreshold {
				m.logger.Warnf("SLOW: %d(%s): %dms", evt.RequestID, evt.CommandName, duration)
				return
			}

			m.logger.Debugf("SUCC: %d(%s): %dms", evt.RequestID, evt.CommandName, duration)
		},
		Failed: func(ctx context.Context, evt *event.CommandFailedEvent) {
			duration := evt.Duration.Milliseconds()

			if m.isExpectedFailure(evt) {
				m.logger.Debugf("FAIL: %d(%s), %s: %dms",
					evt.RequestID,
					evt.CommandName,
					evt.Failure,
					duration,
				)
				return
			}

			// Log unexpected failures as warnings
			m.logger.Warnf("FAIL: %d(%s), %s: %dms",
				evt.RequestID,
				evt.CommandName,
				evt.Failure,
				duration,
			)
		},
	}
}

// isExpectedFailure determines if a command failure is expected and should be logged at debug level.
// This helps reduce noise from normal distributed system operations like leadership competition.
func (m *QueryMonitor) isExpectedFailure(evt *event.CommandFailedEvent) bool {
	if evt.Failure == nil {
		return false
	}

	failureStr := evt.Failure.Error()

	// Check for duplicate key errors which are expected in leadership competition
	if strings.Contains(failureStr, "E11000 duplicate key") && strings.Contains(failureStr, "leaderships") {
		return true
	}

	return false
}

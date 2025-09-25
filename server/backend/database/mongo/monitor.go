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
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/server/logging"
)

// Common MongoDB command and field keys
const (
	filterKey  = "filter"
	queryKey   = "q"
	queryField = "query"
	updatesKey = "updates"
	deletesKey = "deletes"
)

// QueryMonitor represents a MongoDB query monitor.
type QueryMonitor struct {
	logger       logging.Logger
	config       *MonitorConfig
	commandCache *cmap.Map[int64, *commandInfo]
}

// commandInfo stores information about a started command for later use in slow query logging
type commandInfo struct {
	collection string
	operation  string
	filter     string
	startTime  time.Time
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
		logger:       logging.New("mongo"),
		config:       config,
		commandCache: cmap.New[int64, *commandInfo](),
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
			info := m.extractQueryInfo(evt)
			m.commandCache.Set(evt.RequestID, info)

			if logging.Enabled(zap.DebugLevel) {
				m.logger.Debugf("STAR: %d(%s): %s", evt.RequestID, evt.CommandName, evt.Command)
			}
		},
		Succeeded: func(ctx context.Context, evt *event.CommandSucceededEvent) {
			duration := evt.Duration.Milliseconds()
			defer m.cleanupCommand(evt.RequestID)

			if m.config.SlowQueryThreshold > 0 && evt.Duration > m.config.SlowQueryThreshold {
				if info, exists := m.commandCache.Get(evt.RequestID); exists {
					m.logger.Warnf("SLOW: %d %s %s %s: %dms",
						evt.RequestID,
						evt.CommandName,
						info.collection,
						info.filter,
						duration,
					)
				} else {
					m.logger.Warnf("SLOW: %d %s: %dms", evt.RequestID, evt.CommandName, duration)
				}

				return
			}

			m.logger.Debugf("SUCC: %d(%s): %dms", evt.RequestID, evt.CommandName, duration)
		},
		Failed: func(ctx context.Context, evt *event.CommandFailedEvent) {
			m.cleanupCommand(evt.RequestID)

			duration := evt.Duration.Milliseconds()
			if m.isExpectedFailure(evt) {
				m.logger.Debugf("FAIL: %d %s %s: %dms",
					evt.RequestID,
					evt.CommandName,
					evt.Failure,
					duration,
				)
				return
			}

			// Log unexpected failures as warnings
			m.logger.Warnf("FAIL: %d %s %s: %dms",
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
	if strings.Contains(failureStr, "E11000 duplicate key") && strings.Contains(failureStr, "clusternodes") {
		return true
	}

	return false
}

// extractQueryInfo extracts collection name and basic filter information from MongoDB command
func (m *QueryMonitor) extractQueryInfo(evt *event.CommandStartedEvent) *commandInfo {
	info := &commandInfo{
		collection: evt.DatabaseName,
		operation:  evt.CommandName,
		filter:     "{}",
		startTime:  time.Now(),
	}

	// Parse the raw command to extract collection name
	var commandDoc bson.D
	if err := bson.Unmarshal(evt.Command, &commandDoc); err != nil {
		return info
	}
	for _, elem := range commandDoc {
		switch elem.Key {
		case "find", "aggregate", "update", "delete", "findAndModify":
			if collName, ok := elem.Value.(string); ok {
				info.collection = collName
			}
		case filterKey:
			info.filter = m.extractFilterKeys(elem.Value)
		case queryKey:
			info.filter = m.extractFilterKeys(elem.Value)
		case queryField:
			info.filter = m.extractFilterKeys(elem.Value)
		case updatesKey:
			info.filter = m.extractFilterFromArray(elem.Value, queryKey)
		case deletesKey:
			info.filter = m.extractFilterFromArray(elem.Value, queryKey)
		}
	}

	return info
}

// extractFilterKeys extracts just the field names from filter for logging
func (m *QueryMonitor) extractFilterKeys(filter interface{}) string {
	if filter == nil {
		return "{}"
	}

	var keys []string

	switch v := filter.(type) {
	case bson.D:
		for _, elem := range v {
			keys = append(keys, elem.Key)
		}
	case bson.M:
		for key := range v {
			keys = append(keys, key)
		}
	}

	if len(keys) == 0 {
		return "{}"
	}

	return fmt.Sprintf("{%s}", strings.Join(keys, ", "))
}

// extractFilterFromArray extracts filter from MongoDB operation arrays (updates/deletes)
func (m *QueryMonitor) extractFilterFromArray(value interface{}, filterKey string) string {
	if arr, ok := value.(bson.A); ok && len(arr) > 0 {
		if doc, ok := arr[0].(bson.D); ok {
			for _, elem := range doc {
				if elem.Key == filterKey {
					return m.extractFilterKeys(elem.Value)
				}
			}
		}
	}
	return "{}"
}

// cleanupCommand removes command info from cache
func (m *QueryMonitor) cleanupCommand(requestID int64) {
	m.commandCache.Delete(requestID, func(value *commandInfo, exists bool) bool {
		return exists // Delete if exists
	})
}

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

package types

// MetricPoint represents a point of metric data.
type MetricPoint struct {
	// Time is the time of the data.
	Time int64 `json:"time"`

	// Value is the value of the data.
	Value int `json:"value"`
}

// ProjectStats represents the statistics of the project.
type ProjectStats struct {
	// ActiveUsers is the number of active users in the project.
	ActiveUsers []MetricPoint `json:"active_users"`

	// ActiveUsersCount is the number of active users in the project.
	ActiveUsersCount int `json:"active_users_count"`

	// DocumentsCount is the number of documents in the project.
	DocumentsCount int64 `json:"documents_count"`
}

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
	// ActiveUsers is the time-series of active user counts in the project.
	ActiveUsers []MetricPoint `json:"active_users"`

	// ActiveUsersCount is the total count of active users in the project.
	ActiveUsersCount int `json:"active_users_count"`

	// ActiveDocuments is the time-series of active document counts in the project.
	ActiveDocuments []MetricPoint `json:"active_documents"`

	// ActiveDocumentsCount is the total count of active documents in the project.
	ActiveDocumentsCount int `json:"active_documents_count"`

	// ActiveChannels is the time-series of active channel counts in the project.
	ActiveChannels []MetricPoint `json:"active_channels"`

	// ActiveChannelsCount is the total count of active channels in the project.
	ActiveChannelsCount int `json:"active_channels_count"`

	// Sessions is the time-series of session counts in the project.
	Sessions []MetricPoint `json:"sessions"`

	// SessionsCount is the total count of sessions in the project.
	SessionsCount int `json:"sessions_count"`

	// PeakSessionsPerChannel is the time-series of peak sessions per channel counts in the project.
	PeakSessionsPerChannel []MetricPoint `json:"peak_sessions_per_channel"`

	// PeakSessionsPerChannelCount is the total count of peak sessions per channel in the project.
	PeakSessionsPerChannelCount int `json:"peak_sessions_per_channel_count"`

	// DocumentsCount is the number of documents in the project.
	DocumentsCount int64 `json:"documents_count"`

	// ClientsCount is the number of active clients in the project.
	ClientsCount int64 `json:"clients_count"`

	// ChannelsCount is the number of channels in the project.
	ChannelsCount int64 `json:"channels_count"`
}

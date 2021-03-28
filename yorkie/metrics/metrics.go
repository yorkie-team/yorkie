/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package metrics

// Metrics manages the metric information that Yorkie is trying to measure.
type Metrics interface {
	// WithServerVersion adds a server's version information metric.
	WithServerVersion(version string)

	// ObservePushpullResponseSeconds adds response time metrics for PushPull API.
	ObservePushpullResponseSeconds(seconds float64)

	// AddPushpullReceivedChanges adds the number of changes metric
	// included in the request pack of the PushPull API.
	AddPushpullReceivedChanges(count float64)

	// AddPushpullSentChanges adds the number of changes metric
	// included in the response pack of the PushPull API.
	AddPushpullSentChanges(count float64)

	// ObservePushpullSnapshotDurationSeconds adds the time
	// spent metric when taking snapshots.
	ObservePushpullSnapshotDurationSeconds(seconds float64)

	// SetPushpullSnapshotBytes sets the snapshot byte size.
	SetPushpullSnapshotBytes(bytes []byte)
}

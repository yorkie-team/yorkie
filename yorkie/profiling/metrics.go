/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package profiling

import "google.golang.org/grpc"

// Metrics manages the metric information that Yorkie is trying to measure.
type Metrics interface {
	// ObservePushPullResponseSeconds adds response time metrics for PushPull API.
	ObservePushPullResponseSeconds(seconds float64)

	// AddPushPullReceivedChanges sets the number of changes
	// included in the request pack of PushPull.
	AddPushPullReceivedChanges(count int)

	// AddPushPullSentChanges adds the number of changes
	// included in the response pack of PushPull.
	AddPushPullSentChanges(count int)

	// ObservePushPullSnapshotDurationSeconds adds an observation
	// for creating snapshot for the response pack.
	ObservePushPullSnapshotDurationSeconds(seconds float64)

	// AddPushPullSnapshotBytes adds the snapshot byte size of response pack.
	AddPushPullSnapshotBytes(bytes int)

	// RegisterGRPCServer registers the given gRPC server.
	RegisterGRPCServer(server *grpc.Server)
}

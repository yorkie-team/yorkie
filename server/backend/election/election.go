/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

// Package election provides leader election between server cluster. It is used to
// elect leader among server cluster and run tasks only on the leader.
package election

import (
	"context"
	"time"
)

// Elector provides leader election between server cluster. It is used to
// elect leader among server cluster and run tasks only on the leader.
type Elector interface {
	// StartElection starts leader election.
	StartElection(
		leaseLockName string,
		leaseDuration time.Duration,
		onStartLeading func(ctx context.Context),
		onStoppedLeading func(),
	) error

	// Stop stops all leader elections.
	Stop() error
}

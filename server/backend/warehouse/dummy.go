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
	"time"

	"github.com/yorkie-team/yorkie/api/types"
)

// DummyWarehouse is a dummy warehouse that does nothing. It is used when the
// warehouse is not configured.
type DummyWarehouse struct{}

// GetActiveUsers does nothing.
func (w *DummyWarehouse) GetActiveUsers(id types.ID, from, to time.Time) ([]types.MetricPoint, error) {
	return nil, nil
}

// GetActiveUsersCount does nothing.
func (w *DummyWarehouse) GetActiveUsersCount(id types.ID, from, to time.Time) (int, error) {
	return 0, nil
}

// GetDocs does nothing.
func (w *DummyWarehouse) Close() error {
	return nil
}

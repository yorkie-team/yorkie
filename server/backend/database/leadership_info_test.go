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

package database_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestLeadershipInfo(t *testing.T) {
	now := time.Now()

	t.Run("IsExpired should return false for future expiry", func(t *testing.T) {
		info := &database.LeadershipInfo{
			ExpiresAt: now.Add(10 * time.Second),
		}
		assert.False(t, info.IsExpired())
	})

	t.Run("IsExpired should return true for past expiry", func(t *testing.T) {
		info := &database.LeadershipInfo{
			ExpiresAt: now.Add(-10 * time.Second),
		}
		assert.True(t, info.IsExpired())
	})

	t.Run("TimeUntilExpiry should return correct duration", func(t *testing.T) {
		future := now.Add(30 * time.Second)
		info := &database.LeadershipInfo{
			ExpiresAt: future,
		}

		duration := info.TimeUntilExpiry()
		assert.True(t, duration > 25*time.Second)
		assert.True(t, duration <= 30*time.Second)
	})
}

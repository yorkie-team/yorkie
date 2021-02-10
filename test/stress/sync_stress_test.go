// +build stress

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

package stress

import (
	"context"
	gosync "sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/memory"
)

func TestSyncStress(t *testing.T) {
	t.Run("lock/unlock stress test", func(t *testing.T) {
		start := time.Now()

		lockerMap := memory.NewLockerMap()

		size := 100
		sum := 0
		var wg gosync.WaitGroup
		for i := 0; i < size; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				ctx := context.Background()
				locker, err := lockerMap.NewLocker(ctx, sync.Key(t.Name()))
				assert.NoError(t, err)
				assert.NoError(t, locker.Lock(ctx))
				sum += 1
				assert.NoError(t, locker.Unlock(ctx))
			}()
		}
		wg.Wait()
		assert.Equal(t, size, sum)

		log.Logger.Infof("lock count: %d, elapsed: %s", size, time.Since(start))
	})
}

/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package cache_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	pkgcache "github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/server/backend/cache"
)

func TestNew(t *testing.T) {
	valid := cache.Options{
		AuthWebhookCacheSize:         8,
		AuthWebhookCacheTTL:          time.Minute,
		SnapshotCacheSize:            8,
		ChannelSessionCountCacheSize: 8,
		ChannelSessionCountCacheTTL:  time.Minute,
	}

	t.Run("create caches with valid TTLs", func(t *testing.T) {
		_, err := cache.New(valid)
		assert.NoError(t, err)
	})

	t.Run("reject TTLs below MinTTL", func(t *testing.T) {
		opts := valid
		opts.AuthWebhookCacheTTL = 0
		_, err := cache.New(opts)
		assert.ErrorIs(t, err, pkgcache.ErrInvalidTTL)

		opts = valid
		opts.ChannelSessionCountCacheTTL = time.Nanosecond
		_, err = cache.New(opts)
		assert.ErrorIs(t, err, pkgcache.ErrInvalidTTL)
	})
}

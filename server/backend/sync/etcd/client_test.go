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

package etcd_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/sync/etcd"
)

func TestClient(t *testing.T) {
	t.Run("dial timeout test", func(t *testing.T) {
		var err error

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err = etcd.Dial(&etcd.Config{
				Endpoints:   []string{"invalid-endpoint:2379"},
				DialTimeout: "1s",
			}, nil)
		}()
		wg.Wait()

		assert.ErrorAs(t, err, &context.DeadlineExceeded)
	})
}

func TestConfig_Validate(t *testing.T) {
	scenarios := []*struct {
		config   *etcd.Config
		expected error
	}{
		{config: &etcd.Config{Endpoints: []string{}}, expected: etcd.ErrEmptyEndpoints},
		{
			config: &etcd.Config{
				Endpoints:     []string{"localhost:2379"},
				DialTimeout:   "5s",
				LockLeaseTime: "30s",
			},
			expected: nil,
		},
	}
	for _, scenario := range scenarios {
		assert.ErrorIs(
			t, scenario.config.Validate(), scenario.expected, "provided config: %#v", scenario.config)
	}
}

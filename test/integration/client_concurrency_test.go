// +build integration

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

package integration

import (
	"context"
	gosync "sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
)

func TestClientConcurrency(t *testing.T) {
	MAX_ODD_ROUTINES := 5
	MAX_EVEN_ROUTINES := 6

	t.Run("concurrent activate/deactivate test", func(t *testing.T) {
		cli, err := client.Dial(defaultAgent.RPCAddr())
		assert.NoError(t, err)
		defer func() {
			err := cli.Close()
			assert.NoError(t, err)
		}()

		ctx := context.Background()
		wg := gosync.WaitGroup{}

		// Activate <-> Deactivate in goroutine
		// Starts with activate client, Odd times of MAX_ODD_ROUTINES
		for i := 0; i <= MAX_ODD_ROUTINES; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				if idx%2 == 0 {
					assert.NoError(t, cli.Activate(ctx))
					return
				}
				assert.NoError(t, cli.Deactivate(ctx))
			}(i)
		}

		wg.Wait()

		// The final state should be ** Deactive **
		assert.False(t, cli.IsActive())

		// Activate <-> Deactivate in goroutine
		// Starts with activate client, Even times of MAX_EVEN_ROUTINES
		for i := 0; i <= MAX_EVEN_ROUTINES; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				if idx%2 == 0 {
					assert.NoError(t, cli.Activate(ctx))
					return
				}
				assert.NoError(t, cli.Deactivate(ctx))
			}(i)
		}

		wg.Wait()

		// The final state should be ** Active **
		assert.True(t, cli.IsActive())

		err = cli.Deactivate(ctx)
		assert.NoError(t, err)
	})
}

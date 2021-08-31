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
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestClientConcurrency(t *testing.T) {
	t.Run("concurrent activate/deactivate test", func(t *testing.T) {
		MAX_ROUTINES := 5

		cli, err := client.Dial(defaultAgent.RPCAddr())
		assert.NoError(t, err)
		defer func() {
			err := cli.Close()
			assert.NoError(t, err)
		}()

		ctx := context.Background()
		wg := gosync.WaitGroup{}

		// Activate <-> Deactivate in goroutine
		for i := 0; i <= MAX_ROUTINES; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				if idx%2 == 0 {
					assert.NoError(t, cli.Activate(ctx))
				} else {
					assert.NoError(t, cli.Deactivate(ctx))
				}
			}(i)
		}

		wg.Wait()

		if !cli.IsActive() {
			cli.Activate(ctx)
		}

		// All Activate in goroutine
		for i := 0; i <= MAX_ROUTINES; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				assert.NoError(t, cli.Activate(ctx))
			}()
		}

		wg.Wait()

		assert.True(t, cli.IsActive())

		// All Deactivate in goroutine
		for i := 0; i <= MAX_ROUTINES; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				assert.NoError(t, cli.Deactivate(ctx))
			}()
		}

		wg.Wait()

		assert.False(t, cli.IsActive())
	})

	t.Run("concurrent attach/detach intersect test", func(t *testing.T) {
		MAX_EVEN_ROUTINES := 4
		MAX_ODD_ROUTINES := 5

		cli, err := client.Dial(defaultAgent.RPCAddr())
		assert.NoError(t, err)
		defer func() {
			err := cli.Close()
			assert.NoError(t, err)
		}()

		// Base document doc of {"k1", "v1"} settings
		doc := document.New(helper.Collection, t.Name())
		err = doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			return nil
		}, "update k1 with v1")
		assert.NoError(t, err)

		ctx := context.Background()
		wg := gosync.WaitGroup{}

		assert.NoError(t, cli.Activate(ctx))

		// Attach <-> Detach attachments with go routines
		// If num of go routine is ODD => attach tweice + detach twice of same doc
		// ...Means no doc in client
		for i := 0; i <= MAX_EVEN_ROUTINES; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				if idx%2 == 0 {
					assert.NoError(t, cli.Attach(ctx, doc))
				} else {
					assert.NoError(t, cli.Detach(ctx, doc))
				}
			}(i)
		}

		wg.Wait()

		assert.False(t, doc.IsAttached())

		// Attach <-> Detach attachments with go routines
		// If num of go routine is EVEN => attach three times + detach twice of same doc
		// ...Means finally doc in client
		for i := 0; i <= MAX_ODD_ROUTINES; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				if idx%2 == 0 {
					assert.NoError(t, cli.Attach(ctx, doc))
				} else {
					assert.NoError(t, cli.Detach(ctx, doc))
				}
			}(i)
		}

		wg.Wait()

		assert.True(t, doc.IsAttached())
	})

	t.Run("concurrent attach/detach single continuous test", func(t *testing.T) {

	})
}

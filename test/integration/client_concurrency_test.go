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
	"strconv"
	gosync "sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestClientConcurrency(t *testing.T) {
	MAX_ROUTINES := 5

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
		for i := 0; i <= MAX_ROUTINES; i++ {
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

	t.Run("concurrent attach/detach test", func(t *testing.T) {
		cli, err := client.Dial(defaultAgent.RPCAddr())
		assert.NoError(t, err)
		defer func() {
			err := cli.Close()
			assert.NoError(t, err)
		}()

		// Base document doc splice of {"k{i}", "v{i}"} settings
		//doc := document.New(helper.Collection, t.Name())
		doc := make([]*document.Document, MAX_ROUTINES)

		// documents num of MAX_ROUTINES
		for i := 0; i < MAX_ROUTINES; i++ {
			docId := "k" + strconv.Itoa(i)
			docVal := "v" + strconv.Itoa(i)
			doc[i] = document.New(helper.Collection, t.Name()+docId)
			err = doc[i].Update(func(root *proxy.ObjectProxy) error {
				root.SetString(docId, docVal)
				return nil
			}, "update "+docId+" with "+docVal)
			assert.NoError(t, err)
		}

		ctx := context.Background()
		wg := gosync.WaitGroup{}

		assert.NoError(t, cli.Activate(ctx))

		// Attach 5 documents with go routines
		for i := 0; i < MAX_ROUTINES; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				assert.NoError(t, cli.Attach(ctx, doc[idx]))
			}(i)
		}

		wg.Wait()

		for i := 0; i < MAX_ROUTINES; i++ {
			assert.True(t, doc[i].IsAttached())
		}

		// Dettach 5 documents with go routines
		for i := 0; i < MAX_ROUTINES; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				assert.NoError(t, cli.Detach(ctx, doc[idx]))
			}(i)
		}

		wg.Wait()

		for i := 0; i < MAX_ROUTINES; i++ {
			assert.False(t, doc[i].IsAttached())
		}
	})

	t.Run("concurrent watch document across agent with one client in go routines", func(t *testing.T) {

	})
}

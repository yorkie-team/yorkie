//go:build integration

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
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestServer(t *testing.T) {
	t.Run("closing WatchDocument stream on server shutdown test", func(t *testing.T) {
		ctx := context.Background()
		svr := helper.TestServer()
		assert.NoError(t, svr.Start())

		cli, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))

		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, doc, client.WithRealtimeSync()))

		// Subscribe to watch stream to verify it closes properly on server shutdown
		stream, _, err := cli.WatchStream(doc)
		assert.NoError(t, err)

		var closed atomic.Bool

		// Monitor watch stream for server shutdown signal
		go func() {
			for wdr := range stream {
				// Server shutdown should close the stream with CodeCanceled error
				if connect.CodeOf(wdr.Err) == connect.CodeCanceled {
					assert.Len(t, wdr.Presences, 0)
					closed.Store(true)
					return
				}
			}
		}()

		// Shutdown server and verify stream closes within timeout
		assert.NoError(t, svr.Shutdown(true))

		assert.Eventually(t, func() bool {
			return closed.Load()
		}, 3*time.Second, 100*time.Millisecond, "stream should close on server shutdown")
	})
}

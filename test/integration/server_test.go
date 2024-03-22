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
	"sync"
	"testing"

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
		assert.NoError(t, cli.Attach(ctx, doc))

		wg := sync.WaitGroup{}
		wrch, _, err := cli.Subscribe(doc)
		assert.NoError(t, err)

		go func() {
			for {
				select {
				case <-ctx.Done():
					assert.Fail(t, "unexpected ctx done")
					return
				case wr := <-wrch:
					// TODO(hackerwins): Define ClientError instead of using ConnectError later.
					// For now, we use ConnectError to check whether the stream is closed. To
					// simplify the interface, we will define ClientError later.
					if connect.CodeOf(wr.Err) == connect.CodeCanceled {
						assert.Len(t, wr.Presences, 0)
						wg.Done()
						return
					}
				}
			}
		}()

		wg.Add(1)
		assert.NoError(t, svr.Shutdown(true))

		wg.Wait()
	})
}

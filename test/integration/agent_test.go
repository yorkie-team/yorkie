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
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestAgent(t *testing.T) {
	t.Run("closing WatchDocument stream on agent shutdown test", func(t *testing.T) {
		ctx := context.Background()
		agent := helper.TestYorkie()
		assert.NoError(t, agent.Start())

		cli, err := client.Dial(agent.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))

		doc := document.New(helper.Collection, t.Name())
		assert.NoError(t, cli.Attach(ctx, doc))

		wg := sync.WaitGroup{}
		wrch, err := cli.Watch(ctx, doc)
		assert.NoError(t, err)

		go func() {
			for {
				select {
				case <-ctx.Done():
					assert.Fail(t, "unexpected ctx done")
					return
				case wr := <-wrch:
					if wr.Err == io.EOF || status.Code(wr.Err) == codes.Canceled {
						peers := wr.PeersMapByDoc[doc.Key().BSONKey()]
						assert.Len(t, peers, 0)
						wg.Done()
						return
					}
				}
			}
		}()

		wg.Add(1)
		assert.NoError(t, agent.Shutdown(true))

		wg.Wait()
	})
}

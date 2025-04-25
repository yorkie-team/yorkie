//go:build bench

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

package bench

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

func benchmarkPresenceConcurrency(b *testing.B, svr *server.Yorkie, initialCnt int, concurrentCnt int, syncCnt int) {
	ctx := context.Background()
	docKey := key.Key(fmt.Sprintf("presence-concurrency-bench-%d-%d-%d-%d", initialCnt, concurrentCnt, syncCnt, b.N))

	// 01. Prepare n clients and attach them to the document.
	clients, _, err := helper.ClientsAndAttachedDocs(ctx, svr.RPCAddr(), docKey, initialCnt)
	assert.NoError(b, err)
	helper.CleanupClients(b, clients)

	// 02. Then create new clients and attach them to the document concurrently.
	var wg sync.WaitGroup
	for range concurrentCnt {
		wg.Add(1)
		go func() {
			defer wg.Done()

			client, doc, err := helper.ClientAndAttachedDoc(ctx, svr.RPCAddr(), docKey)
			if err != nil {
				b.Logf("client and doc creation: %v", err)
				return
			}

			assert.NoError(b, client.Sync(ctx))

			for i := range syncCnt {
				assert.NoError(b, doc.Update(func(root *json.Object, p *presence.Presence) error {
					p.Set("key", fmt.Sprintf("%d", i))
					return nil
				}))
			}
			assert.NoError(b, client.Sync(ctx))
			assert.NoError(b, client.Close())
		}()
	}
	wg.Wait()
}

func BenchmarkPresenceConcurrency(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	// NOTE(hackerwins): To prevent the snapshot from being created, we set
	// snapshot threshold and snapshot interval to very large values.
	svr, err := helper.TestServerWithSnapshotCfg(100_000, 100_000)
	if err != nil {
		b.Fatal(err)
	}

	b.Cleanup(func() {
		if err := svr.Shutdown(true); err != nil {
			b.Fatal(err)
		}
	})

	b.ResetTimer()

	b.Run("0-100-10", func(b *testing.B) {
		benchmarkPresenceConcurrency(b, svr, 0, 100, 10)
	})

	b.Run("100-100-10", func(b *testing.B) {
		benchmarkPresenceConcurrency(b, svr, 100, 100, 10)
	})

	b.Run("300-100-10", func(b *testing.B) {
		benchmarkPresenceConcurrency(b, svr, 300, 100, 10)
	})
}

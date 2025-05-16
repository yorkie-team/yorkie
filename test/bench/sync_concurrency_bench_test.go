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

func benchmarkSyncConcurrency(b *testing.B, svr *server.Yorkie, initialCnt int, concurrentCnt int, syncCnt int) {
	ctx := context.Background()
	docKey := key.Key(fmt.Sprintf("sync-concurrency-bench-%d-%d-%d", initialCnt, concurrentCnt, syncCnt))

	// 01. Create n clients and attach them to the document sequentially.
	clients, docs, err := helper.ClientsAndAttachedDocs(ctx, svr.RPCAddr(), docKey, initialCnt)
	assert.NoError(b, err)
	docs[0].Update(func(r *json.Object, p *presence.Presence) error {
		r.SetNewObject("field")
		return nil
	})
	assert.NoError(b, clients[0].Sync(ctx))
	helper.CleanupClients(b, clients)

	// 02. Then create new clients and attach them to the document concurrently.
	var wg sync.WaitGroup
	for range concurrentCnt {
		wg.Add(1)
		go func() {
			defer wg.Done()

			client, doc, err := helper.ClientAndAttachedDoc(ctx, svr.RPCAddr(), docKey)
			assert.NoError(b, err)

			for range syncCnt {
				doc.Update(func(r *json.Object, p *presence.Presence) error {
					r.GetObject("field").SetBool("key", true)
					return nil
				})
				assert.NoError(b, client.Sync(ctx))
			}
			assert.NoError(b, client.Close())
		}()
	}
	wg.Wait()
}

func BenchmarkSyncConcurrency(b *testing.B) {
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

	b.Run("1-100-10", func(b *testing.B) {
		benchmarkSyncConcurrency(b, svr, 1, 100, 10)
	})

	b.Run("100-100-10", func(b *testing.B) {
		benchmarkSyncConcurrency(b, svr, 100, 100, 10)
	})

	b.Run("300_100-10", func(b *testing.B) {
		benchmarkSyncConcurrency(b, svr, 300, 100, 10)
	})
}

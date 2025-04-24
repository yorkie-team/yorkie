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

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

var loadTestServer *server.Yorkie

func benchmarkSyncConcurrency(b *testing.B, seqCnt int, concurrentCnt int, syncCnt int) {
	b.ResetTimer()

	var mu sync.Mutex
	var activeClients []*client.Client

	ctx := context.Background()
	docKey := key.Key(fmt.Sprintf("sync-concurrency-bench-%d-%d-%d", seqCnt, concurrentCnt, syncCnt))

	// 01. Create n clients and attach them to the document sequentially.
	clients, _, err := helper.ClientsAndAttachedDocs(ctx, loadTestServer.RPCAddr(), docKey, seqCnt)
	assert.NoError(b, err)
	activeClients = append(activeClients, clients...)

	// 02. Then create new clients and attach them to the document concurrently.
	var wg sync.WaitGroup
	for range concurrentCnt {
		wg.Add(1)
		go func() {
			defer wg.Done()

			client, _, err := helper.ClientAndAttachedDoc(ctx, loadTestServer.RPCAddr(), docKey)
			assert.NoError(b, err)

			mu.Lock()
			activeClients = append(activeClients, client)
			mu.Unlock()

			for range syncCnt {
				err := client.Sync(ctx)
				assert.NoError(b, err)
			}
		}()
	}
	wg.Wait()

	b.StopTimer()
	helper.CleanupClients(b, activeClients)
}

func benchmarkChangeConcurrency(b *testing.B, seqCnt int, concurrentCnt int, syncCnt int) {
	b.ResetTimer()

	var mu sync.Mutex
	var activeClients []*client.Client

	ctx := context.Background()
	docKey := key.Key(fmt.Sprintf("change-concurrency-bench-%d-%d-%d-%d", seqCnt, concurrentCnt, syncCnt, b.N))

	// 01. Create n clients and attach them to the document sequentially.
	clients, docs, err := helper.ClientsAndAttachedDocs(ctx, loadTestServer.RPCAddr(), docKey, seqCnt)
	assert.NoError(b, err)
	err = docs[0].Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("text")
		return nil
	})
	assert.NoError(b, err)
	assert.Equal(b, `{"text":[]}`, docs[0].Marshal())
	clients[0].Sync(ctx)

	// 02. Then create new clients and attach them to the document concurrently.
	var wg sync.WaitGroup
	for range concurrentCnt {
		wg.Add(1)
		go func() {
			defer wg.Done()

			client, doc, err := helper.ClientAndAttachedDoc(ctx, loadTestServer.RPCAddr(), docKey)
			assert.NoError(b, err)

			mu.Lock()
			activeClients = append(activeClients, client)
			mu.Unlock()

			err = client.Sync(ctx)
			assert.NoError(b, err)
			for range syncCnt {
				err = doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetText("text").Edit(0, 0, "a")
					return nil
				})
				assert.NoError(b, err)
			}
			err = client.Sync(ctx)
			assert.NoError(b, err)
		}()
	}
	wg.Wait()

	b.StopTimer()
	helper.CleanupClients(b, activeClients)
}

func BenchmarkSyncConcurrency(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	// NOTE(hackerwins): To prevent the snapshot from being created, we set
	// snapshot threshold and snapshot interval to very large values.
	svr, err := helper.TestServerWithSnapshotCfg(100_000, 100_000)
	if err != nil {
		b.Fatal(err)
	}
	loadTestServer = svr

	defer func() {
		if err := svr.Shutdown(true); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	b.Run("clients_0_100", func(b *testing.B) {
		benchmarkSyncConcurrency(b, 0, 100, 100)
	})

	b.Run("clients_100_100", func(b *testing.B) {
		benchmarkSyncConcurrency(b, 100, 100, 100)
	})

	b.Run("clients_300_100", func(b *testing.B) {
		benchmarkSyncConcurrency(b, 300, 100, 100)
	})

	b.Run("clients_500_100", func(b *testing.B) {
		benchmarkSyncConcurrency(b, 500, 100, 100)
	})
}

func BenchmarkChangeConcurrency(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	// NOTE(hackerwins): To prevent the snapshot from being created, we set
	// snapshot threshold and snapshot interval to very large values.
	svr, err := helper.TestServerWithSnapshotCfg(100_000, 100_000)
	if err != nil {
		b.Fatal(err)
	}
	loadTestServer = svr

	defer func() {
		if err := svr.Shutdown(true); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	b.Run("clients_1_100_100", func(b *testing.B) {
		benchmarkChangeConcurrency(b, 1, 100, 100)
	})

	b.Run("clients_100_100_300", func(b *testing.B) {
		benchmarkChangeConcurrency(b, 100, 100, 300)
	})
}

func BenchmarkChangeConcurrencyWithSnapshot(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	svr, err := helper.TestServerWithSnapshotCfg(1, 1)
	if err != nil {
		b.Fatal(err)
	}
	loadTestServer = svr

	defer func() {
		if err := svr.Shutdown(true); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	b.Run("clients_1_100_100", func(b *testing.B) {
		benchmarkChangeConcurrency(b, 1, 100, 100)
	})

	b.Run("clients_100_100_300", func(b *testing.B) {
		benchmarkChangeConcurrency(b, 100, 100, 300)
	})

	b.Run("clients_100_100_1000", func(b *testing.B) {
		benchmarkChangeConcurrency(b, 100, 100, 1000)
	})
}

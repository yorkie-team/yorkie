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
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

var loadTestServer *server.Yorkie

func startLoadTestServer(snapshotInterval int64, snapshotThreshold int64) error {
	config := helper.TestConfig()
	config.Backend.SnapshotInterval = snapshotInterval
	config.Backend.SnapshotThreshold = snapshotThreshold

	svr, err := server.New(config)
	if err != nil {
		return err
	}
	if err := svr.Start(); err != nil {
		return err
	}

	loadTestServer = svr
	return nil
}

func createDocKeyForSyncOnlyLoadTest(preparedClientCnt int, clientCnt int) key.Key {
	return key.Key(fmt.Sprintf("sync-only-load-test-bench-%d-%d-%d", preparedClientCnt, clientCnt, gotime.Now().UnixMilli()))
}

func initializeClientAndDocForLoadTest(
	ctx context.Context,
	docKey key.Key,
) (*client.Client, *document.Document, error) {
	c, err := client.Dial(loadTestServer.RPCAddr())
	if err != nil {
		return nil, nil, err
	}
	if err := c.Activate(ctx); err != nil {
		return nil, nil, err
	}
	d := document.New(docKey)
	if err := c.Attach(ctx, d); err != nil {
		return nil, nil, err
	}
	return c, d, nil
}

func initializeClientsAndDocsForLoadTest(
	ctx context.Context,
	b *testing.B,
	n int,
	docKey key.Key,
) ([]*client.Client, []*document.Document) {
	var clients []*client.Client
	var docs []*document.Document
	for i := 0; i < n; i++ {
		c, d, err := initializeClientAndDocForLoadTest(ctx, docKey)
		assert.NoError(b, err)
		clients = append(clients, c)
		docs = append(docs, d)
	}
	return clients, docs
}

func benchmarkSyncOnlyLoadTest(
	preparedClientCnt int,
	clientCnt int,
	b *testing.B,
) {
	var activeClients []*client.Client
	var mu sync.Mutex

	ctx := context.Background()
	docKey := createDocKeyForSyncOnlyLoadTest(preparedClientCnt, clientCnt)

	// 1. Create a document with prepared clients.
	clients, _ := initializeClientsAndDocsForLoadTest(ctx, b, preparedClientCnt, docKey)
	activeClients = append(activeClients, clients...)

	// 2. Create a document with n clients.
	var wg sync.WaitGroup
	for i := 0; i < clientCnt; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, _, err := initializeClientAndDocForLoadTest(ctx, docKey)
			assert.NoError(b, err)

			mu.Lock()
			activeClients = append(activeClients, client)
			mu.Unlock()

			for i := 0; i < 100; i++ {
				err := client.Sync(ctx)
				assert.NoError(b, err)
			}
		}()
	}
	wg.Wait()

	for _, c := range activeClients {
		assert.NoError(b, c.Close())
	}
}

func BenchmarkSyncOnlyLoadTest(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))
	assert.NoError(b, startLoadTestServer(100000, 100000))

	defer func() {
		if loadTestServer == nil {
			return
		}

		if err := loadTestServer.Shutdown(true); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	b.Run("clients_0_100", func(b *testing.B) {
		benchmarkSyncOnlyLoadTest(0, 100, b)
	})

	b.Run("clients_100_100", func(b *testing.B) {
		benchmarkSyncOnlyLoadTest(100, 100, b)
	})
}

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

func startLoadTestServer() {
	config := helper.TestConfig()
	// config.Mongo = nil

	svr, err := server.New(config)
	if err != nil {
		logging.DefaultLogger().Fatal(err)
	}
	if err := svr.Start(); err != nil {
		logging.DefaultLogger().Fatal(err)
	}
	loadTestServer = svr
}

func createDocKeyForSyncOnlyLoadTest(preparedClientCnt int, clientCnt int) key.Key {
	return key.Key(fmt.Sprintf("sync-only-load-test-bench-%d-%d-%d", preparedClientCnt, clientCnt, gotime.Now().UnixMilli()))
}

func initializeClientAndDocForLoadTest(
	ctx context.Context,
	b *testing.B,
	docKey key.Key,
) (*client.Client, *document.Document, error) {
	c, err := client.Dial(
		loadTestServer.RPCAddr(),
	)
	if err != nil {
		return nil, nil, err
	}
	err = c.Activate(ctx)
	if err != nil {
		return nil, nil, err
	}
	d := document.New(docKey)
	err = c.Attach(ctx, d)
	if err != nil {
		return nil, nil, err
	}

	return c, d, nil
}

func initializeClientsAndDocsForLoadTest(
	ctx context.Context,
	b *testing.B,
	n int,
	docKey key.Key,
) ([]*client.Client, []*document.Document, error) {
	var clients []*client.Client
	var docs []*document.Document
	for i := 0; i < n; i++ {
		c, d, err := initializeClientAndDocForLoadTest(ctx, b, docKey)
		if err != nil {
			logging.DefaultLogger().Error(err)
			continue
		}
		clients = append(clients, c)
		docs = append(docs, d)
	}
	return clients, docs, nil
}

func benchmarkSyncOnlyLoadTest(
	preparedClientCnt int,
	clientCnt int,
	b *testing.B,
) {
	// 1. Create a document with prepared clients.
	ctx := context.Background()
	docKey := createDocKeyForSyncOnlyLoadTest(preparedClientCnt, clientCnt)
	_, _, err := initializeClientsAndDocsForLoadTest(ctx, b, preparedClientCnt, docKey)
	if err != nil {
		logging.DefaultLogger().Error(err)
	}

	// 2. Create a document with n clients.
	var wg sync.WaitGroup
	for i := 0; i < clientCnt; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, _, err := initializeClientAndDocForLoadTest(ctx, b, docKey)
			if err != nil {
				logging.DefaultLogger().Error(err)
				return
			}
			for i := 0; i < 30; i++ {
				err := client.Sync(ctx)
				if err != nil {
					logging.DefaultLogger().Error(err)
				}
				gotime.Sleep(100 * gotime.Millisecond)
			}
		}()
	}
	wg.Wait()
}

func BenchmarkSyncOnlyLoadTest(b *testing.B) {
	err := logging.SetLogLevel("error")
	assert.NoError(b, err)
	startLoadTestServer() // default config
	defer func() {
		if loadTestServer == nil {
			return
		}

		if err := loadTestServer.Shutdown(true); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	b.Run("clients 0-100", func(b *testing.B) {
		benchmarkSyncOnlyLoadTest(0, 100, b)
	})

	b.Run("clients 100-100", func(b *testing.B) {
		benchmarkSyncOnlyLoadTest(100, 100, b)
	})

	b.Run("clients 500-100", func(b *testing.B) {
		benchmarkSyncOnlyLoadTest(500, 100, b)
	})

	b.Run("clients 1000-100", func(b *testing.B) {
		benchmarkSyncOnlyLoadTest(1000, 100, b)
	})

	b.Run("clients 2500-100", func(b *testing.B) {
		benchmarkSyncOnlyLoadTest(2500, 100, b)
	})

	b.Run("clients 5000-100", func(b *testing.B) {
		benchmarkSyncOnlyLoadTest(5000, 100, b)
	})

	b.Run("clients 10000-100", func(b *testing.B) {
		benchmarkSyncOnlyLoadTest(10000, 100, b)
	})
}

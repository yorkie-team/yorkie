//go:build bench

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

package bench

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

type clientAndDocPair struct {
	cli *client.Client
	doc *document.Document
}

var defaultServer *server.Yorkie

func TestMain(m *testing.M) {
	svr := helper.TestServer()
	if err := svr.Start(); err != nil {
		logging.DefaultLogger().Fatal(err)
	}
	defaultServer = svr
	code := m.Run()
	if defaultServer != nil {
		if err := defaultServer.Shutdown(true); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}
	os.Exit(code)
}

// activeClient is a helper function to create active clients.
func activeClients(B *testing.B, n int) (clients []*client.Client) {
	for i := 0; i < n; i++ {
		c, err := client.Dial(
			defaultServer.RPCAddr(),
			client.WithPresence(types.Presence{"name": fmt.Sprintf("name-%d", i)}),
		)
		assert.NoError(B, err)

		err = c.Activate(context.Background())
		assert.NoError(B, err)

		clients = append(clients, c)
	}
	return
}

// cleanupClients is a helper function to clean up clients.
func cleanupClients(b *testing.B, clients []*client.Client) {
	for _, c := range clients {
		assert.NoError(b, c.Deactivate(context.Background()))
		assert.NoError(b, c.Close())
	}
}

func syncClientsThenAssertEqual(t *testing.B, pairs []clientAndDocPair) {
	assert.True(t, len(pairs) > 1)
	ctx := context.Background()
	// Save own changes and get previous changes.
	for i, pair := range pairs {
		fmt.Printf("before d%d: %s\n", i+1, pair.doc.Marshal())
		err := pair.cli.Sync(ctx)
		assert.NoError(t, err)
	}

	// Get last client changes.
	// Last client get all precede changes in above loop.
	for _, pair := range pairs[:len(pairs)-1] {
		err := pair.cli.Sync(ctx)
		assert.NoError(t, err)
	}

	// Assert start.
	expected := pairs[0].doc.Marshal()
	fmt.Printf("after d1: %s\n", expected)
	for i, pair := range pairs[1:] {
		v := pair.doc.Marshal()
		fmt.Printf("after d%d: %s\n", i+2, v)
		assert.Equal(t, expected, v)
	}
}


func BenchmarkRPC(b *testing.B) {
	svr, err := server.New(helper.TestConfig())
	assert.NoError(b, err)
	assert.NoError(b, svr.Start())
	defer func() { assert.NoError(b, svr.Shutdown(true)) }()

	clients := activeClients(b, 2)
	c1, c2 := clients[0], clients[1]
	defer cleanupClients(b, clients)
	b.Run("rpc between 2 client test", func(b *testing.B) {
		ctx := context.Background()
		d1 := document.New(key.Key(b.Name()))
		err := c1.Attach(ctx, d1)
		assert.NoError(b, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k1")
			return nil
		}, "set v1 by c1")
		assert.NoError(b, err)
		assert.Equal(b, `{"k1":{}}`, d1.Marshal())
		err = c1.Sync(ctx)
		assert.NoError(b, err)

		d2 := document.New(key.Key(b.Name()))
		err = c2.Attach(ctx, d2)
		assert.NoError(b, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.Delete("k1")
			root.SetString("k1", "v1")
			return nil
		}, "delete and set v1 by c1")
		assert.NoError(b, err)
		assert.Equal(b, `{"k1":"v1"}`, d1.Marshal())

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.Delete("k1")
			root.SetString("k1", "v2")
			return nil
		}, "delete and set v2 by c2")
		assert.NoError(b, err)
		assert.Equal(b, `{"k1":"v2"}`, d2.Marshal())
		syncClientsThenAssertEqual(b, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})
}

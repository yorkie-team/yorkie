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
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

func BenchmarkDeletionConcurrency(b *testing.B) {
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

	// Multiple clients insert 100 characters (text); one deletes a 10-character range while other inserts single characters concurrently
	b.Run("concurrent text delete range 100", func(b *testing.B) {
		benchmarkConcurrentTextDeleteRange(b, svr, 100, 10)
	})

	// Multiple clients insert 1,000 characters (text); one deletes a 10-character range while other inserts single characters concurrently
	b.Run("concurrent text delete range 1000", func(b *testing.B) {
		benchmarkConcurrentTextDeleteRange(b, svr, 1000, 10)
	})

	// Multiple clients insert 100 characters (tree); one deletes a 10-character range while other inserts single characters concurrently
	b.Run("concurrent tree delete range 100", func(b *testing.B) {
		benchmarkConcurrentTreeDeleteRange(b, svr, 100, 10)
	})

	// Multiple clients insert 1,000 characters (tree); one deletes a 10-character range while other inserts single characters concurrently
	b.Run("concurrent tree delete range 1000", func(b *testing.B) {
		benchmarkConcurrentTreeDeleteRange(b, svr, 1000, 10)
	})

	// Multiple clients insert 100 characters (text); one client deletes everything at once
	b.Run("concurrent text edit delete all 100", func(b *testing.B) {
		benchmarkConcurrentTextDeleteAll(b, svr, 100, 10)
	})

	// Multiple clients insert 1,000 characters (text); one client deletes everything at once
	b.Run("concurrent text edit delete all 1000", func(b *testing.B) {
		benchmarkConcurrentTextDeleteAll(b, svr, 1000, 10)
	})

	// Multiple clients insert 100 characters (tree); one client deletes everything at once
	b.Run("concurrent tree edit delete all 100", func(b *testing.B) {
		benchmarkConcurrentTreeDeleteAll(b, svr, 100, 10)
	})

	// Multiple clients insert 1,000 characters (tree); one client deletes everything at once
	b.Run("concurrent tree edit delete all 1000", func(b *testing.B) {
		benchmarkConcurrentTreeDeleteAll(b, svr, 1000, 10)
	})
}

func benchmarkConcurrentTextDeleteRange(b *testing.B, svr *server.Yorkie, cnt, clientCount int) {
	for i := 0; i < b.N; i++ {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("text-bench-%d-%d", i, gotime.Now().UnixMilli()))

		// 1. Activate n clients and attach all clients to the document
		clients, docs, err := helper.ClientsAndAttachedDocs(ctx, svr.RPCAddr(), docKey, clientCount)
		assert.NoError(b, err)

		// 2. Initialize the text
		clients[0].Sync(ctx)
		err = docs[0].Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1")
			return nil
		})
		assert.NoError(b, err)
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Sync(ctx))
		}
		assert.Equal(b, `{"k1":[]}`, docs[0].Marshal())
		assert.Equal(b, `{"k1":[]}`, docs[clientCount-1].Marshal())

		// 3. Each client performs edits
		for k := 0; k < cnt; k++ {
			// Calculate which client's turn it is
			clientIdx := k % clientCount
			assert.NoError(b, clients[clientIdx].Sync(ctx))
			err := docs[clientIdx].Update(func(root *json.Object, p *presence.Presence) error {
				text := root.GetText("k1")
				text.Edit(k, k, "a")
				return nil
			})
			assert.NoError(b, err)
			assert.NoError(b, clients[clientIdx].Sync(ctx))
		}
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Sync(ctx))
		}
		assert.Equal(b, docs[0].Root().GetText("k1").Marshal(), docs[clientCount-1].Root().GetText("k1").Marshal())

		// 4. One client performs deletions and other client performs insertions
		deleteRangeSize := 10
		deleteCount := cnt / deleteRangeSize
		for k := 0; k < deleteCount; k++ {
			clientIdx := k % clientCount
			clientIdx2 := (clientIdx + 1) % clientCount
			assert.NoError(b, clients[clientIdx].Sync(ctx))
			assert.NoError(b, clients[clientIdx2].Sync(ctx))

			err := docs[clientIdx].Update(func(root *json.Object, p *presence.Presence) error {
				text := root.GetText("k1")
				text.Edit(k, k+deleteRangeSize, "")
				return nil
			})
			assert.NoError(b, err)

			err = docs[clientIdx2].Update(func(root *json.Object, p *presence.Presence) error {
				text := root.GetText("k1")
				text.Edit(k+1, k+1, "b")
				return nil
			})
			assert.NoError(b, err)

			assert.NoError(b, clients[clientIdx].Sync(ctx))
			assert.NoError(b, clients[clientIdx2].Sync(ctx))
		}
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Sync(ctx))
		}
		assert.Equal(b, docs[0].Root().GetText("k1").Marshal(), docs[clientCount-1].Root().GetText("k1").Marshal())

		// 5. Cleanup
		helper.CleanupClients(b, clients)
	}
}

func benchmarkConcurrentTreeDeleteRange(b *testing.B, svr *server.Yorkie, cnt, clientCount int) {
	for i := 0; i < b.N; i++ {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("tree-bench-%d-%d", i, gotime.Now().UnixMilli()))

		// 1. Activate n clients and attach all clients to the document
		clients, docs, err := helper.ClientsAndAttachedDocs(ctx, svr.RPCAddr(), docKey, clientCount)
		assert.NoError(b, err)

		// 2. Initialize the tree
		clients[0].Sync(ctx)
		err = docs[0].Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{},
				}},
			})
			return nil
		})
		assert.NoError(b, err)
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Sync(ctx))
		}

		// 3. Each client performs edits
		for k := 0; k < cnt; k++ {
			// Calculate which client's turn it is
			clientIdx := k % clientCount
			assert.NoError(b, clients[clientIdx].Sync(ctx))
			err := docs[clientIdx].Update(func(root *json.Object, p *presence.Presence) error {
				tree := root.GetTree("t")
				tree.Edit(k+1, k+1, &json.TreeNode{Type: "text", Value: "a"}, 0)
				return nil
			})
			assert.NoError(b, err)
			assert.NoError(b, clients[clientIdx].Sync(ctx))
		}
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Sync(ctx))
		}
		assert.Equal(b, docs[0].Root().GetTree("t").Marshal(), docs[clientCount-1].Root().GetTree("t").Marshal())

		// 4. One client performs deletions and other client performs insertions
		deleteRangeSize := 10
		deleteCount := cnt / deleteRangeSize
		for k := 1; k < deleteCount+1; k++ {
			clientIdx := k % clientCount
			clientIdx2 := (clientIdx + 1) % clientCount
			assert.NoError(b, clients[clientIdx].Sync(ctx))
			assert.NoError(b, clients[clientIdx2].Sync(ctx))

			err := docs[clientIdx].Update(func(root *json.Object, p *presence.Presence) error {
				tree := root.GetTree("t")
				tree.Edit(k, k+deleteRangeSize, nil, 0)
				return nil
			})
			assert.NoError(b, err)

			err = docs[clientIdx2].Update(func(root *json.Object, p *presence.Presence) error {
				tree := root.GetTree("t")
				tree.Edit(k+1, k+1, &json.TreeNode{Type: "text", Value: "b"}, 0)
				return nil
			})
			assert.NoError(b, err)

			assert.NoError(b, clients[clientIdx].Sync(ctx))
			assert.NoError(b, clients[clientIdx2].Sync(ctx))
		}
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Sync(ctx))
		}
		assert.Equal(b, docs[0].Root().GetTree("t").Marshal(), docs[clientCount-1].Root().GetTree("t").Marshal())

		// 5. Cleanup
		helper.CleanupClients(b, clients)
	}
}

func benchmarkConcurrentTextDeleteAll(b *testing.B, svr *server.Yorkie, cnt, clientCount int) {
	for i := 0; i < b.N; i++ {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("text-bench-%d-%d", i, gotime.Now().UnixMilli()))

		// 1. Activate n clients and attach all clients to the document
		clients, docs, err := helper.ClientsAndAttachedDocs(ctx, svr.RPCAddr(), docKey, clientCount)
		assert.NoError(b, err)

		// 2. Initialize the text
		clients[0].Sync(ctx)
		err = docs[0].Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1")
			return nil
		})
		assert.NoError(b, err)
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Sync(ctx))
		}
		assert.Equal(b, `{"k1":[]}`, docs[0].Marshal())
		assert.Equal(b, `{"k1":[]}`, docs[clientCount-1].Marshal())

		// 3. Each client performs edits
		for k := 0; k < cnt; k++ {
			// Calculate which client's turn it is
			clientIdx := k % clientCount
			assert.NoError(b, clients[clientIdx].Sync(ctx))
			err := docs[clientIdx].Update(func(root *json.Object, p *presence.Presence) error {
				text := root.GetText("k1")
				text.Edit(k, k, "a")
				return nil
			})
			assert.NoError(b, err)
			assert.NoError(b, clients[clientIdx].Sync(ctx))
		}
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Sync(ctx))
		}
		assert.Equal(b, docs[0].Root().GetText("k1").Marshal(), docs[clientCount-1].Root().GetText("k1").Marshal())

		// 4. Client0 deletes all
		err = docs[0].Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText("k1")
			text.Edit(0, cnt, "")
			return nil
		}, "Delete all at a time")
		assert.NoError(b, err)
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Sync(ctx))
		}
		assert.Equal(b, "[]", docs[0].Root().GetText("k1").Marshal())
		assert.Equal(b, "[]", docs[clientCount-1].Root().GetText("k1").Marshal())

		// 5. Cleanup
		helper.CleanupClients(b, clients)
	}
}

func benchmarkConcurrentTreeDeleteAll(b *testing.B, svr *server.Yorkie, cnt, clientCount int) {
	for i := 0; i < b.N; i++ {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("tree-bench-%d-%d", i, gotime.Now().UnixMilli()))

		// 1. Activate n clients and attach all clients to the document
		clients, docs, err := helper.ClientsAndAttachedDocs(ctx, svr.RPCAddr(), docKey, clientCount)
		assert.NoError(b, err)

		// 2. Initialize the tree
		clients[0].Sync(ctx)
		err = docs[0].Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{},
				}},
			})
			return nil
		})
		assert.NoError(b, err)
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Sync(ctx))
		}

		// 3. Each client performs edits
		for k := 0; k < cnt; k++ {
			// Calculate which client's turn it is
			clientIdx := k % clientCount
			assert.NoError(b, clients[clientIdx].Sync(ctx))
			err := docs[clientIdx].Update(func(root *json.Object, p *presence.Presence) error {
				tree := root.GetTree("t")
				tree.Edit(k+1, k+1, &json.TreeNode{Type: "text", Value: "a"}, 0)
				return nil
			})
			assert.NoError(b, err)
			assert.NoError(b, clients[clientIdx].Sync(ctx))
		}
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Sync(ctx))
		}
		assert.Equal(b, docs[0].Root().GetTree("t").Marshal(), docs[clientCount-1].Root().GetTree("t").Marshal())

		// 4. Client0 deletes all
		err = docs[0].Update(func(root *json.Object, p *presence.Presence) error {
			tree := root.GetTree("t")
			tree.Edit(1, cnt+1, nil, 0)
			return nil
		})
		assert.NoError(b, err)
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Sync(ctx))
		}
		assert.Equal(b, `{"type":"root","children":[{"type":"p","children":[]}]}`, docs[0].Root().GetTree("t").Marshal())
		assert.Equal(b, `{"type":"root","children":[{"type":"p","children":[]}]}`, docs[clientCount-1].Root().GetTree("t").Marshal())

		// 5. Cleanup
		helper.CleanupClients(b, clients)
	}
}

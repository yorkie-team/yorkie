//go:build bench

/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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
	"strings"
	"sync"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

func benchmarkUpdateAndSync(
	b *testing.B,
	ctx context.Context,
	cnt int,
	cli *client.Client,
	d *document.Document,
	key string,
) {
	for range cnt {
		err := d.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText(key)
			text.Edit(0, 0, "c")
			return nil
		})
		assert.NoError(b, err)
		err = cli.Sync(ctx)
		assert.NoError(b, err)
	}
}

func benchmarkUpdateProject(ctx context.Context, b *testing.B, cnt int, adminCli *admin.Client, project *types.Project) error {
	for i := range cnt {
		name := fmt.Sprintf("name%d-%d", i, gotime.Now().UnixMilli())
		authWebhookURL := fmt.Sprintf("http://authWebhookURL%d", i)
		var authWebhookMethods []string
		for _, m := range types.AuthMethods() {
			authWebhookMethods = append(authWebhookMethods, string(m))
		}
		authWebhookMaxRetries := uint64(10)
		authWebhookMinWaitInterval := "10ms"
		authWebhookMaxWaitInterval := "1s"
		authWebhookRequestTimeout := "2s"
		eventWebhookURL := fmt.Sprintf("http://eventWebhookURL%d", i)
		eventWebhookEvents := []string{string(types.DocRootChanged)}
		eventWebhookMaxRetries := uint64(20)
		eventWebhookMinWaitInterval := "8ms"
		eventWebhookMaxWaitInterval := "3s"
		eventWebhookRequestTimeout := "5s"
		clientDeactivateThreshold := "1h"
		snapshotThreshold := int64(50)
		snapshotInterval := int64(10)

		_, err := adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				Name:                        &name,
				AuthWebhookURL:              &authWebhookURL,
				AuthWebhookMethods:          &authWebhookMethods,
				AuthWebhookMaxRetries:       &authWebhookMaxRetries,
				AuthWebhookMinWaitInterval:  &authWebhookMinWaitInterval,
				AuthWebhookMaxWaitInterval:  &authWebhookMaxWaitInterval,
				AuthWebhookRequestTimeout:   &authWebhookRequestTimeout,
				EventWebhookURL:             &eventWebhookURL,
				EventWebhookEvents:          &eventWebhookEvents,
				EventWebhookMaxRetries:      &eventWebhookMaxRetries,
				EventWebhookMinWaitInterval: &eventWebhookMinWaitInterval,
				EventWebhookMaxWaitInterval: &eventWebhookMaxWaitInterval,
				EventWebhookRequestTimeout:  &eventWebhookRequestTimeout,
				ClientDeactivateThreshold:   &clientDeactivateThreshold,
				SnapshotThreshold:           &snapshotThreshold,
				SnapshotInterval:            &snapshotInterval,
			},
		)
		assert.NoError(b, err)
	}
	return nil
}

func BenchmarkRPC(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	svr := helper.TestServerWithSnapshotCfg(helper.SnapshotInterval, helper.SnapshotThreshold)
	b.Cleanup(func() {
		if err := svr.Shutdown(true); err != nil {
			b.Fatal(err)
		}
	})

	b.Run("client to server", func(b *testing.B) {
		ctx := context.Background()
		cli, doc, err := helper.ClientAndAttachedDoc(ctx, svr.RPCAddr(), "doc1")
		assert.NoError(b, err)

		for range b.N {
			testKey := "testKey"
			err = doc.Update(func(r *json.Object, p *presence.Presence) error {
				r.SetNewText(testKey)
				return nil
			})
			assert.NoError(b, err)

			benchmarkUpdateAndSync(b, ctx, 100, cli, doc, testKey)
		}

		assert.NoError(b, cli.Close())
	})

	b.Run("client to client via server", func(b *testing.B) {
		clients := helper.ActiveClients(b, svr.RPCAddr(), 2)
		c1, c2 := clients[0], clients[1]
		defer helper.CleanupClients(b, clients)

		ctx := context.Background()

		// Setup documents with initial content
		d1 := document.New(helper.TestDocKey(b))
		err := c1.Attach(ctx, d1, client.WithRealtimeSync())
		assert.NoError(b, err)
		testKey1 := "testKey1"
		err = d1.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewText(testKey1)
			return nil
		})
		assert.NoError(b, err)

		d2 := document.New(helper.TestDocKey(b))
		err = c2.Attach(ctx, d2, client.WithRealtimeSync())
		assert.NoError(b, err)
		testKey2 := "testKey2"
		err = d2.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewText(testKey2)
			return nil
		})
		assert.NoError(b, err)

		// Benchmark: Both clients update their documents concurrently
		for range b.N {
			wg := sync.WaitGroup{}
			wg.Add(2)

			// Client 1 updates its document
			go func() {
				defer wg.Done()
				benchmarkUpdateAndSync(b, ctx, 50, c1, d1, testKey1)
			}()

			// Client 2 updates its document
			go func() {
				defer wg.Done()
				benchmarkUpdateAndSync(b, ctx, 50, c2, d2, testKey2)
			}()

			wg.Wait()
		}
	})

	b.Run("attach large document", func(b *testing.B) {
		str := strings.Repeat("a", 10485000)

		for range b.N {
			func() {
				clients := helper.ActiveClients(b, svr.RPCAddr(), 2)
				c1, c2 := clients[0], clients[1]
				defer helper.CleanupClients(b, clients)

				ctx := context.Background()
				doc1 := document.New(helper.TestDocKey(b))
				doc2 := document.New(helper.TestDocKey(b))

				err := doc1.Update(func(r *json.Object, p *presence.Presence) error {
					text := r.SetNewText("k1")
					text.Edit(0, 0, str)
					return nil
				})
				assert.NoError(b, err)
				err = doc2.Update(func(r *json.Object, p *presence.Presence) error {
					text := r.SetNewText("k1")
					text.Edit(0, 0, str)
					return nil
				})
				assert.NoError(b, err)

				wg := sync.WaitGroup{}
				wg.Add(2)
				go func() {
					defer wg.Done()
					err := c1.Attach(ctx, doc1)
					assert.NoError(b, err)
				}()
				go func() {
					defer wg.Done()
					err := c2.Attach(ctx, doc2)
					assert.NoError(b, err)
				}()
				wg.Wait()
			}()
		}
	})

	b.Run("adminCli to server", func(b *testing.B) {
		adminCli := helper.CreateAdminCli(b, svr.RPCAddr())
		defer func() { adminCli.Close() }()

		ctx := context.Background()
		project, err := adminCli.CreateProject(ctx, "admin-cli-test")
		assert.NoError(b, err)

		for range b.N {
			assert.NoError(b, benchmarkUpdateProject(ctx, b, 500, adminCli, project))
		}
	})
}

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
	"io"
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
	for i := 0; i < cnt; i++ {
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
	for i := 0; i < cnt; i++ {
		name := fmt.Sprintf("name%d-%d", i, gotime.Now().UnixMilli())
		authWebhookURL := fmt.Sprintf("http://authWebhookURL%d", i)
		var authWebhookMethods []string
		for _, m := range types.AuthMethods() {
			authWebhookMethods = append(authWebhookMethods, string(m))
		}
		eventWebhookURL := fmt.Sprintf("http://eventWebhookURL%d", i)
		eventWebhookEvents := []string{string(types.DocRootChanged)}
		clientDeactivateThreshold := "1h"

		_, err := adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				Name:                      &name,
				AuthWebhookURL:            &authWebhookURL,
				AuthWebhookMethods:        &authWebhookMethods,
				EventWebhookURL:           &eventWebhookURL,
				EventWebhookEvents:        &eventWebhookEvents,
				ClientDeactivateThreshold: &clientDeactivateThreshold,
			},
		)
		assert.NoError(b, err)
	}
	return nil
}

func watchDoc(
	ctx context.Context,
	b *testing.B,
	cli *client.Client,
	d *document.Document,
	rch <-chan client.WatchResponse,
	done <-chan bool,
) {
	for {
		select {
		case resp := <-rch:
			if resp.Err == io.EOF {
				assert.Fail(b, resp.Err.Error())
			}
			assert.NoError(b, resp.Err)

			if resp.Type == client.DocumentChanged {
				err := cli.Sync(ctx, client.WithDocKey(d.Key()))
				assert.NoError(b, err)
			}
		case <-done:
			return
		}
	}
}

func BenchmarkRPC(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	svr := helper.TestServer()
	assert.NoError(b, svr.Start())
	b.Cleanup(func() {
		if err := svr.Shutdown(true); err != nil {
			b.Fatal(err)
		}
	})

	b.Run("client to server", func(b *testing.B) {
		ctx := context.Background()
		cli, doc, err := helper.ClientAndAttachedDoc(ctx, svr.RPCAddr(), "doc1")
		assert.NoError(b, err)

		for i := 0; i < b.N; i++ {
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

		rch1, _, err := c1.Subscribe(d1)
		assert.NoError(b, err)
		rch2, _, err := c2.Subscribe(d2)
		assert.NoError(b, err)

		done1 := make(chan bool)
		done2 := make(chan bool)

		for i := 0; i < b.N; i++ {
			wg := sync.WaitGroup{}
			wg.Add(2)
			go func() {
				defer wg.Done()
				watchDoc(ctx, b, c1, d1, rch1, done2)
			}()
			go func() {
				defer wg.Done()
				watchDoc(ctx, b, c2, d2, rch2, done1)
			}()

			go func() {
				benchmarkUpdateAndSync(b, ctx, 50, c1, d1, testKey1)
				done1 <- true
			}()
			go func() {
				benchmarkUpdateAndSync(b, ctx, 50, c2, d2, testKey2)
				done2 <- true
			}()

			wg.Wait()
		}
	})

	b.Run("attach large document", func(b *testing.B) {
		str := strings.Repeat("a", 10485000)

		for i := 0; i < b.N; i++ {
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

		for i := 0; i < b.N; i++ {
			assert.NoError(b, benchmarkUpdateProject(ctx, b, 500, adminCli, project))
		}
	})
}

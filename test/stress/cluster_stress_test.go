// +build stress

package stress

import (
	"context"
	"math/rand"
	"strconv"
	gosync "sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/test/helper"
	"github.com/yorkie-team/yorkie/yorkie"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/memory"
)

func TestClusterStress(t *testing.T) {
	t.Run("watch document test", func(t *testing.T) {
		testerSize := 5
		agents := make([]*yorkie.Yorkie, testerSize)

		for i := 0; i < testerSize; i++ {
			agent := helper.TestYorkie()
			assert.NoError(t, agent.Start())
			agents[i] = agent
		}

		defer func() {
			for _, agent := range agents {
				assert.NoError(t, agent.Shutdown(true))
			}
		}()

		clients := make([]*client.Client, testerSize)
		docs := make([]*document.Document, testerSize)
		ctx := context.Background()
		for i, agent := range agents {
			cli, err := client.Dial(agent.RPCAddr(), client.Option{
				Metadata: map[string]string{
					"key": strconv.Itoa(i),
				},
			})
			assert.NoError(t, err)

			assert.NoError(t, cli.Activate(ctx))
			clients[i] = cli

			doc := document.New(helper.Collection, t.Name())
			assert.NoError(t, cli.Attach(ctx, doc))
			docs[i] = doc

			err = doc.Update(func(root *proxy.ObjectProxy) error {
				root.SetString("hello", "world")
				root.SetNewText("text")
				root.SetNewArray("arr")

				return nil
			})
			assert.NoError(t, err)
		}

		for _, cli := range clients {
			err := cli.Sync(ctx)
			assert.NoError(t, err)
		}

		defer func() {
			for _, cli := range clients {
				assert.NoError(t, cli.Deactivate(ctx))
				assert.NoError(t, cli.Close())
			}
		}()

		assertForDocs := func(docs []*document.Document) {
			var expected string
			for i, doc := range docs {
				if i == 0 {
					expected = doc.Marshal()
					continue
				}

				assert.Equal(t, expected, doc.Marshal())
			}
		}

		requestCount := 100
		wg := &gosync.WaitGroup{}
		wg.Add(1)

		locks := memory.NewLockerMap()
		for i, cli := range clients {
			rch := cli.Watch(ctx, docs[i])
			go func(cli *client.Client) {
				timeoutCount := 0
				for {
					select {
					case resp := <-rch:
						timeoutCount = 0
						if resp.EventType == types.DocumentsChangeEvent {
							locker, err := locks.NewLocker(ctx, sync.NewKey(cli.Metadata()["key"]))
							assert.NoError(t, err)

							err = locker.Lock(ctx)
							assert.NoError(t, err)

							err = cli.Sync(ctx, resp.Keys...)
							assert.NoError(t, err)

							err = locker.Unlock(ctx)
							assert.NoError(t, err)
						}
					case <-time.After(time.Second):
						timeoutCount++
						if timeoutCount > 10 {
							// A 'nagative WaitGroup counter' error occurs intermittently, causing a panic.
							defer func() {
								if r := recover(); r != nil {
									assertForDocs(docs)
								}
							}()
							wg.Done()
							return
						}
					}
				}
			}(cli)
		}

		for i := 0; i < requestCount; i++ {
			testerIdx := rand.Intn(testerSize)
			cli := clients[testerIdx]

			locker, err := locks.NewLocker(ctx, sync.NewKey(cli.Metadata()["key"]))
			assert.NoError(t, err)

			err = locker.Lock(ctx)
			assert.NoError(t, err)
			doc := docs[testerIdx]
			err = doc.Update(func(root *proxy.ObjectProxy) error {
				root.SetString("test"+strconv.Itoa(testerIdx), "yorkie")
				root.GetText("text").Edit(0, 0, strconv.Itoa(testerIdx))
				root.GetArray("arr").AddInteger(testerIdx)
				return nil
			}, "update "+strconv.Itoa(i)+"::"+strconv.Itoa(testerIdx))
			assert.NoError(t, err)

			err = cli.Sync(ctx)
			assert.NoError(t, err)

			err = locker.Unlock(ctx)
			assert.NoError(t, err)
		}

		wg.Wait()

		assertForDocs(docs)
	})
}

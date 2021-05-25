package stress

import (
	"context"
	"github.com/yorkie-team/yorkie/pkg/log"
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
)

func TestClusterStress(t *testing.T) {
	t.Run("watch document test", func(t *testing.T) {
		testerSize := 5
		agents := make([]*yorkie.Yorkie, testerSize)

		for i := 0; i < testerSize; i++ {
			agent := helper.TestYorkie((i + 1) * 1000)
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
			cli, err := client.Dial(agent.RPCAddr())
			assert.NoError(t, err)

			assert.NoError(t, cli.Activate(ctx))
			clients[i] = cli

			doc := document.New(helper.Collection, t.Name())
			assert.NoError(t, cli.Attach(ctx, doc))
			docs[i] = doc

			err = doc.Update(func(root *proxy.ObjectProxy) error {
				root.SetString("hello", "world")

				return nil
			})
			assert.NoError(t, err)

			err = cli.Sync(ctx)
			assert.NoError(t, err)
		}

		defer func() {
			for _, cli := range clients {
				assert.NoError(t, cli.Deactivate(ctx))
				assert.NoError(t, cli.Close())
			}
		}()

		requestCount := 100
		wg := &gosync.WaitGroup{}
		waitCount := requestCount * 10
		wg.Add(requestCount * 10)

		doneCount := 0

		start := time.Now()
		var duration int64
		for i, cli := range clients {
			rch := cli.Watch(ctx, docs[i])

			go func(cli *client.Client) {
				for {
					select {
					case resp := <-rch:
						if resp.EventType == types.DocumentsChangeEvent {
							err := cli.Sync(ctx, resp.Keys...)
							assert.NoError(t, err)
							doneCount++
							wg.Done()
						}
					case <-time.After(5 * time.Second):
						end := time.Now()
						duration = end.Sub(start).Milliseconds()
						for i := 0; i < waitCount-doneCount; i++ {
							wg.Done()
						}
						return
					}
				}
			}(cli)
		}

		for i := 0; i < requestCount; i++ {
			testerIdx := rand.Intn(testerSize)
			doc := docs[0]
			err := doc.Update(func(root *proxy.ObjectProxy) error {
				root.SetString("test"+strconv.Itoa(testerIdx), "yorkie")
				return nil
			})
			assert.NoError(t, err)

			cli := clients[0]
			err = cli.Sync(ctx)
			assert.NoError(t, err)
		}

		wg.Wait()

		var expected string
		for i, doc := range docs {
			if i == 0 {
				expected = doc.Marshal()
				continue
			}

			assert.Equal(t, expected, doc.Marshal())
		}

		log.Logger.Infof("Duration %d ms", duration)
	})
}

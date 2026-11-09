//go:build integration

/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestChannelBatchWindow sends two broadcasts back to back and times their
// arrival: the first goes out at once on an idle channel, and the second
// waits for the rest of the project's window.
func TestChannelBatchWindow(t *testing.T) {
	svr, err := server.New(helper.TestConfig())
	require.NoError(t, err)
	require.NoError(t, svr.Start())
	defer func() { _ = svr.Shutdown(true) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()

	ctx := context.Background()

	projects := 0
	// newProject creates a project with the given window; empty leaves it
	// unset, so the server default applies.
	newProject := func(t *testing.T, window string) *types.Project {
		projects++
		prj, err := adminCli.CreateProject(ctx, fmt.Sprintf("batch-window-%d", projects))
		require.NoError(t, err)
		if window == "" {
			return prj
		}
		_, err = adminCli.UpdateProject(ctx, prj.ID.String(), &types.UpdatableProjectFields{
			ChannelBatchWindow: &window,
		})
		require.NoError(t, err)
		return prj
	}

	// join attaches a new client of the project to the channel and watches
	// it. Received broadcasts land on the returned channel.
	join := func(t *testing.T, prj *types.Project, k string) (*channel.Channel, chan time.Time) {
		cli, err := client.Dial(svr.RPCAddr(), client.WithAPIKey(prj.PublicKey))
		require.NoError(t, err)
		t.Cleanup(func() { _ = cli.Close() })
		require.NoError(t, cli.Activate(ctx))
		t.Cleanup(func() { _ = cli.Deactivate(ctx) })

		ch, err := channel.New(key.Key(k))
		require.NoError(t, err)
		require.NoError(t, cli.Attach(ctx, ch))
		counts, closeWatch, err := cli.WatchChannel(ctx, ch)
		require.NoError(t, err)
		t.Cleanup(closeWatch)
		<-counts

		got := make(chan time.Time, 10)
		ch.SubscribeBroadcastEvent("t", func(_, _ string, _ []byte) error {
			got <- time.Now()
			return nil
		})
		return ch, got
	}

	// gap broadcasts twice from sender and returns how long after the first
	// arrival the second arrived at the receiver.
	gap := func(t *testing.T, sender *channel.Channel, got chan time.Time) time.Duration {
		// Let any delivery started by joining (session counts) end its window.
		time.Sleep(1200 * time.Millisecond)
		for len(got) > 0 {
			<-got
		}

		recv := func() time.Time {
			select {
			case ts := <-got:
				return ts
			case <-time.After(3 * time.Second):
				require.FailNow(t, "broadcast not delivered")
				return time.Time{}
			}
		}

		// Send the second only after the first arrived, so they cannot ride
		// in one batch: then only the window can hold the second back.
		require.NoError(t, sender.Broadcast("t", 1))
		first := recv()
		require.NoError(t, sender.Broadcast("t", 2))
		return recv().Sub(first)
	}

	t.Run("each project delivers on its own window", func(t *testing.T) {
		slow := newProject(t, "1s")
		fast := newProject(t, "0s")

		slowSender, _ := join(t, slow, "room")
		_, slowGot := join(t, slow, "room")
		fastSender, _ := join(t, fast, "room")
		_, fastGot := join(t, fast, "room")

		assert.GreaterOrEqual(t, gap(t, slowSender, slowGot), 700*time.Millisecond)
		assert.Less(t, gap(t, fastSender, fastGot), 300*time.Millisecond)
	})

	t.Run("a project without a window uses the server default", func(t *testing.T) {
		prj := newProject(t, "")
		sender, _ := join(t, prj, "room")
		_, got := join(t, prj, "room")

		// The test server keeps the 100ms default.
		g := gap(t, sender, got)
		assert.GreaterOrEqual(t, g, 50*time.Millisecond)
		assert.Less(t, g, 400*time.Millisecond)
	})

	t.Run("a changed window reaches an open channel with its next subscriber", func(t *testing.T) {
		prj := newProject(t, "1s")
		sender, _ := join(t, prj, "room")
		_, got := join(t, prj, "room")
		assert.GreaterOrEqual(t, gap(t, sender, got), 700*time.Millisecond)

		window := "0s"
		_, err := adminCli.UpdateProject(ctx, prj.ID.String(), &types.UpdatableProjectFields{
			ChannelBatchWindow: &window,
		})
		require.NoError(t, err)
		join(t, prj, "room")

		assert.Less(t, gap(t, sender, got), 300*time.Millisecond)
	})
}

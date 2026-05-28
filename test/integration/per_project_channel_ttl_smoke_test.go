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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestPerProjectChannelSessionTTLConcurrent verifies that two projects with
// different ChannelSessionTTL values applied simultaneously expire their
// channel sessions on their own clocks.
//
// Setup:
//   - Server with the test default 1s cleanup interval.
//   - Project A: ChannelSessionTTL = "2s".
//   - Project B: ChannelSessionTTL = "5s".
//   - Two Go clients, one per project, each with WithChannelHeartbeatInterval
//     set to 30s so that the auto-refresh loop does NOT fire during the test
//     window. This lets the per-project TTL govern alone.
//
// Expectations:
//   - Both channels start with 1 session.
//   - After ~3.5s: Project A's session expired (>2s elapsed past its 2s TTL +
//     ≤1s cleanup tick), Project B's still alive (<5s elapsed).
//   - After ~7s total: Project B's session also expired.
func TestPerProjectChannelSessionTTLConcurrent(t *testing.T) {
	svr, err := server.New(helper.TestConfig())
	require.NoError(t, err)
	require.NoError(t, svr.Start())
	defer func() { _ = svr.Shutdown(true) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()

	ctx := context.Background()

	projA, err := adminCli.CreateProject(ctx, "ttl-concurrent-a")
	require.NoError(t, err)
	ttlA := "2s"
	_, err = adminCli.UpdateProject(ctx, projA.ID.String(), &types.UpdatableProjectFields{
		ChannelSessionTTL: &ttlA,
	})
	require.NoError(t, err)

	projB, err := adminCli.CreateProject(ctx, "ttl-concurrent-b")
	require.NoError(t, err)
	ttlB := "5s"
	_, err = adminCli.UpdateProject(ctx, projB.ID.String(), &types.UpdatableProjectFields{
		ChannelSessionTTL: &ttlB,
	})
	require.NoError(t, err)

	// 30s heartbeat ensures no auto-refresh fires during the test window.
	cliA, err := client.Dial(
		svr.RPCAddr(),
		client.WithAPIKey(projA.PublicKey),
		client.WithChannelHeartbeatInterval(30*time.Second),
	)
	require.NoError(t, err)
	defer func() { _ = cliA.Close() }()
	require.NoError(t, cliA.Activate(ctx))

	cliB, err := client.Dial(
		svr.RPCAddr(),
		client.WithAPIKey(projB.PublicKey),
		client.WithChannelHeartbeatInterval(30*time.Second),
	)
	require.NoError(t, err)
	defer func() { _ = cliB.Close() }()
	require.NoError(t, cliB.Activate(ctx))

	chA, err := channel.New("room-A")
	require.NoError(t, err)
	require.NoError(t, cliA.Attach(ctx, chA))

	chB, err := channel.New("room-B")
	require.NoError(t, err)
	require.NoError(t, cliB.Attach(ctx, chB))

	mgr := svr.Backend().Channel
	refKeyA := types.ChannelRefKey{ProjectID: projA.ID, ChannelKey: "room-A"}
	refKeyB := types.ChannelRefKey{ProjectID: projB.ID, ChannelKey: "room-B"}

	// 1. Both sessions live.
	assert.Equal(t, int64(1), mgr.SessionCount(refKeyA, false), "projA initial session count")
	assert.Equal(t, int64(1), mgr.SessionCount(refKeyB, false), "projB initial session count")

	// 2. Wait past projA's 2s TTL + one cleanup tick (1s), but well below
	//    projB's 5s. Use Eventually so a slow CI cleanup tick does not flake.
	assert.Eventually(t, func() bool {
		return mgr.SessionCount(refKeyA, false) == 0
	}, 4*time.Second, 200*time.Millisecond, "projA session should expire on its 2s TTL")
	assert.Equal(t, int64(1), mgr.SessionCount(refKeyB, false), "projB session must still be alive at the projA-expiry checkpoint")

	// 3. Wait past projB's 5s TTL too (total elapsed ≤ 8s + cleanup margin).
	assert.Eventually(t, func() bool {
		return mgr.SessionCount(refKeyB, false) == 0
	}, 5*time.Second, 200*time.Millisecond, "projB session should expire on its 5s TTL")
}

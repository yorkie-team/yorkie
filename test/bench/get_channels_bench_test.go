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
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/test/helper"
)

// channelScenarioConfig defines the configuration for a channel benchmark scenario.
type channelScenarioConfig struct {
	// Channel Structure
	channel1LevelCount int // Number of level-1 channels
	channel2LevelCount int // Number of level-2 channels per level-1
	channel3LevelCount int // Number of level-3 channels per level-2

	// Query Sizes for GetChannels API
	level1ChannelQueryCount int // Query N level-1 channels
	level2ChannelQueryCount int // Query N level-2 channels
	level3ChannelQueryCount int // Query N level-3 channels
}

// getLevel1Channels returns level-1 channel keys to query.
func getLevel1Channels(channelPrefix string, cfg *channelScenarioConfig) []string {
	keys := make([]string, 0, cfg.level1ChannelQueryCount)
	interval := max(1, cfg.channel1LevelCount/cfg.level1ChannelQueryCount)

	for i := 0; i < cfg.level1ChannelQueryCount; i++ {
		channelIdx := (i * interval) % cfg.channel1LevelCount
		keys = append(keys, fmt.Sprintf("%s-%d", channelPrefix, channelIdx))
	}

	return keys
}

// getLevel2Channels returns level-2 channel keys for the given parent channel.
func getLevel2Channels(cfg *channelScenarioConfig, parentChannel string) []string {
	keys := make([]string, 0, cfg.level2ChannelQueryCount)
	count := min(cfg.level2ChannelQueryCount, cfg.channel2LevelCount)

	for i := 0; i < count; i++ {
		keys = append(keys, fmt.Sprintf("%s.room-%d", parentChannel, i))
	}

	return keys
}

// getLevel3Channels returns level-3 channel keys for the given parent channel.
func getLevel3Channels(cfg *channelScenarioConfig, parentChannel string) []string {
	keys := make([]string, 0, cfg.level3ChannelQueryCount)
	count := min(cfg.level3ChannelQueryCount, cfg.channel3LevelCount)

	for i := 0; i < count; i++ {
		keys = append(keys, fmt.Sprintf("%s.user-%d", parentChannel, i))
	}

	return keys
}

// selectLevel1ChannelByIndex selects a level-1 channel using deterministic distribution.
func selectLevel1ChannelByIndex(channels []string, userIndex int) string {
	if len(channels) == 0 {
		return ""
	}
	return channels[userIndex%len(channels)]
}

// selectLevel2ChannelByIndex selects a level-2 channel using deterministic distribution.
func selectLevel2ChannelByIndex(cfg *channelScenarioConfig, channels []string, userIndex int) string {
	if len(channels) == 0 {
		return ""
	}
	idx := (userIndex / cfg.channel1LevelCount) % len(channels)
	return channels[idx]
}

// selectLevel3ChannelByIndex selects a level-3 channel using deterministic distribution.
func selectLevel3ChannelByIndex(cfg *channelScenarioConfig, channels []string, userIndex int) string {
	if len(channels) == 0 {
		return ""
	}
	idx := (userIndex / (cfg.channel1LevelCount * cfg.channel2LevelCount)) % len(channels)
	return channels[idx]
}

// runUserJourney simulates a complete user journey through the hierarchical channel system.
func runUserJourney(
	b *testing.B,
	ctx context.Context,
	rpcAddr string,
	adminClient v1connect.AdminServiceClient,
	channelPrefix string,
	cfg *channelScenarioConfig,
	userIndex int,
) (*client.Client, *channel.Channel, *channel.Channel, error) {
	cli, err := client.Dial(rpcAddr)
	assert.NoError(b, err)
	assert.NoError(b, cli.Activate(ctx))

	// Stage 1: Main Page (Level-1)
	level1Channels := getLevel1Channels(channelPrefix, cfg)
	selectedLevel1 := selectLevel1ChannelByIndex(level1Channels, userIndex)
	_, err = adminClient.GetChannels(
		ctx,
		connect.NewRequest(&api.GetChannelsRequest{
			ChannelKeys:    level1Channels,
			IncludeSubPath: true,
		}),
	)
	assert.NoError(b, err)

	// Stage 2: Channel Page (Level-2)
	level2Channels := getLevel2Channels(cfg, selectedLevel1)
	selectedLevel2 := selectLevel2ChannelByIndex(cfg, level2Channels, userIndex)

	// Stage 2-1: Attach to level-2 channel
	ch2, err := channel.New(key.Key(selectedLevel2))
	assert.NoError(b, err)
	assert.NoError(b, cli.Attach(ctx, ch2))

	// Stage 2-2: Get level-2 channels
	_, err = adminClient.GetChannels(
		ctx,
		connect.NewRequest(&api.GetChannelsRequest{
			ChannelKeys:    level2Channels,
			IncludeSubPath: true,
		}),
	)
	assert.NoError(b, err)

	// Stage 3: Sub-Channel Page (Level-3)
	level3Channels := getLevel3Channels(cfg, selectedLevel2)
	selectedLevel3 := selectLevel3ChannelByIndex(cfg, level3Channels, userIndex)

	// Stage 3-1: Attach to level-3 channel
	ch3, err := channel.New(key.Key(selectedLevel3))
	assert.NoError(b, err)
	assert.NoError(b, cli.Attach(ctx, ch3))

	// Stage 3-2: Get level-3 channels
	_, err = adminClient.GetChannels(
		ctx,
		connect.NewRequest(&api.GetChannelsRequest{
			ChannelKeys:    level3Channels,
			IncludeSubPath: true,
		}),
	)
	assert.NoError(b, err)
	return cli, ch2, ch3, nil
}

// benchmarkUserJourneyScenario runs the user journey benchmark for a given scenario.
func benchmarkUserJourneyScenario(
	b *testing.B,
	svr *server.Yorkie,
	cfg *channelScenarioConfig,
) {
	adminConn := http.DefaultClient
	testAdminAuthInterceptor := admin.NewAuthInterceptor("")
	testAdminClient := v1connect.NewAdminServiceClient(
		adminConn,
		"http://"+svr.RPCAddr(),
		connect.WithInterceptors(testAdminAuthInterceptor),
	)

	ctx := context.Background()

	// Admin login
	resp, err := testAdminClient.LogIn(
		ctx,
		connect.NewRequest(&api.LogInRequest{
			Username: helper.AdminUser,
			Password: helper.AdminPassword,
		}),
	)
	assert.NoError(b, err)
	testAdminAuthInterceptor.SetToken(resp.Msg.Token)

	// Get project context
	resp1, err := testAdminClient.GetProject(
		ctx,
		connect.NewRequest(&api.GetProjectRequest{
			Name: "default",
		}),
	)
	assert.NoError(b, err)

	project := converter.FromProject(resp1.Msg.Project)
	ctx = projects.With(ctx, project)

	channelPrefix := b.Name()
	parts := strings.Split(b.Name(), "/")
	if len(parts) > 1 {
		channelPrefix = parts[1]
	}

	b.ResetTimer()

	// Use iteration index as userIndex for deterministic but varying channel selection.
	// Each iteration tests a different channel path, ensuring varied load distribution.
	iterationIdx := 0
	type ClientAndChannelPair struct {
		cli *client.Client
		ch2 *channel.Channel
		ch3 *channel.Channel
	}
	var channelPairs []ClientAndChannelPair
	for range b.N {
		cli, ch2, ch3, err := runUserJourney(b, ctx, svr.RPCAddr(), testAdminClient, channelPrefix, cfg, iterationIdx)
		assert.NoError(b, err)
		channelPairs = append(channelPairs, ClientAndChannelPair{cli, ch2, ch3})
		iterationIdx++
	}

	b.StopTimer()

	b.Cleanup(func() {
		for _, channelPair := range channelPairs {
			assert.NoError(b, channelPair.cli.Detach(ctx, channelPair.ch2))
			assert.NoError(b, channelPair.cli.Detach(ctx, channelPair.ch3))
			assert.NoError(b, channelPair.cli.Deactivate(ctx))
			assert.NoError(b, channelPair.cli.Close())
		}
	})
}

// BenchmarkGetChannels benchmarks the GetChannels API in a realistic user journey scenario.
// This simulates users navigating through a 3-level hierarchical channel structure:
//   - Stage 1: Main page: explore all level-1 channels and select one and join it.
//   - Stage 2: Channel page: explore level-2 channels under the selected level-1 channel and select one and join it.
//   - Stage 3: Sub-channel page: explore level-3 channels under the selected level-2 channel.
func BenchmarkGetChannels(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	svr := helper.TestServerWithSnapshotCfg(helper.SnapshotInterval, helper.SnapshotThreshold)
	b.Cleanup(func() {
		if err := svr.Shutdown(true); err != nil {
			b.Fatal(err)
		}
	})

	// small scenario: 5 × 5 × 5 = 125 channels
	smallCfg := &channelScenarioConfig{
		channel1LevelCount:      5,
		channel2LevelCount:      5,
		channel3LevelCount:      5,
		level1ChannelQueryCount: 5,
		level2ChannelQueryCount: 5,
		level3ChannelQueryCount: 5,
	}

	b.Run("5x5x5_channels", func(b *testing.B) {
		benchmarkUserJourneyScenario(b, svr, smallCfg)
	})

	// medium scenario: 10 × 5 × 5 = 250 channels
	mediumCfg := &channelScenarioConfig{
		channel1LevelCount:      10,
		channel2LevelCount:      5,
		channel3LevelCount:      5,
		level1ChannelQueryCount: 10,
		level2ChannelQueryCount: 5,
		level3ChannelQueryCount: 5,
	}

	b.Run("10x5x5_channels", func(b *testing.B) {
		benchmarkUserJourneyScenario(b, svr, mediumCfg)
	})

	// large scenario: 10 × 5 × 10 = 500 channels
	largeCfg := &channelScenarioConfig{
		channel1LevelCount:      10,
		channel2LevelCount:      5,
		channel3LevelCount:      10,
		level1ChannelQueryCount: 10,
		level2ChannelQueryCount: 5,
		level3ChannelQueryCount: 10,
	}

	b.Run("10x5x10_channels", func(b *testing.B) {
		benchmarkUserJourneyScenario(b, svr, largeCfg)
	})

	// very large scenario: 10 × 10 × 10 = 1000 channels
	veryLargeCfg := &channelScenarioConfig{
		channel1LevelCount:      10,
		channel2LevelCount:      10,
		channel3LevelCount:      10,
		level1ChannelQueryCount: 10,
		level2ChannelQueryCount: 10,
		level3ChannelQueryCount: 10,
	}

	b.Run("10x10x10_channels", func(b *testing.B) {
		benchmarkUserJourneyScenario(b, svr, veryLargeCfg)
	})
}

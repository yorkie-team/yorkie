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
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/backend/channel"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

type mockPubSub struct {
	events []events.ChannelEvent
}

func (m *mockPubSub) PublishChannel(ctx context.Context, event events.ChannelEvent) {
	m.events = append(m.events, event)
}

func benchmarkChannelAttachDetach(b *testing.B, svr *server.Yorkie, clientCount int) {
	ctx := context.Background()

	b.ResetTimer()

	for i := range b.N {
		b.StopTimer()

		// Generate channel keys and create document keys
		channelKey := key.Key(fmt.Sprintf("channel-test-attach-detach-bench-%d-%d", clientCount, i))

		b.StartTimer()
		// Create and attach all clients
		clients, presences, err := helper.ClientsAndAttacheChannels(ctx, svr.RPCAddr(), channelKey, clientCount)
		assert.NoError(b, err)

		// Measure detach performance
		for j := 0; j < clientCount; j++ {
			assert.NoError(b, clients[j].Detach(ctx, presences[j]))
		}
		b.StopTimer()

		helper.CleanupClients(b, clients)
	}
}

func BenchmarkPresencePathOperations(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	svr := helper.TestServerWithSnapshotCfg(100_000, 100_000)
	b.Cleanup(func() {
		if err := svr.Shutdown(true); err != nil {
			b.Fatal(err)
		}
	})

	b.Run("attach-detach-10", func(b *testing.B) {
		benchmarkChannelAttachDetach(b, svr, 10)
	})

	b.Run("attach-detach-50", func(b *testing.B) {
		benchmarkChannelAttachDetach(b, svr, 50)
	})

	b.Run("attach-detach-250", func(b *testing.B) {
		benchmarkChannelAttachDetach(b, svr, 250)
	})
}

// generateHierarchicalKeyPaths generates all leaf key paths for a hierarchical structure.
// For example, with levelCounts = [2, 3], it generates:
// sub-0/sub-0, sub-0/sub-1, sub-0/sub-2, sub-1/sub-0, sub-1/sub-1, sub-1/sub-2
func generateHierarchicalKeyPaths(prefix string, levelCounts []int) []string {
	if len(levelCounts) == 0 {
		return []string{prefix}
	}

	var keyPaths []string
	currentLevelCount := levelCounts[0]
	remainingLevels := levelCounts[1:]

	for i := 0; i < currentLevelCount; i++ {
		var currentPath string
		if prefix == "" {
			currentPath = fmt.Sprintf("sub-%d", i)
		} else {
			currentPath = fmt.Sprintf("%s/sub-%d", prefix, i)
		}

		if len(remainingLevels) == 0 {
			// Leaf node
			keyPaths = append(keyPaths, currentPath)
		} else {
			// Recurse for next level
			subPaths := generateHierarchicalKeyPaths(currentPath, remainingLevels)
			keyPaths = append(keyPaths, subPaths...)
		}
	}

	return keyPaths
}

func parseAndMergeKeyPath(key key.Key) []string {
	keyPaths := channel.ParseKeyPath(key)
	mergedKeyPaths := make([]string, 0, len(keyPaths))
	for i, keyPath := range keyPaths {
		if i == 0 {
			mergedKeyPaths = append(mergedKeyPaths, keyPath)
			continue
		}
		mergedKeyPaths = append(mergedKeyPaths, channel.MergeKeyPath([]string{mergedKeyPaths[i], keyPath}))
	}

	return mergedKeyPaths
}

// benchmarkHierarchicalPresenceCount measures count performance with hierarchical presence structure.
// levelCounts defines the number of nodes at each depth level.
// For example, levelCounts=[1, 10, 20] creates:
// - 1 root node (sub-0)
// - 10 intermediate nodes (sub-0/sub-0 ~ sub-0/sub-9)
// - 200 leaf nodes (sub-0/sub-0/sub-0 ~ sub-0/sub-9/sub-19)
// Count all Presence count of paths root to last leaf(sub-0 ~ sub-0/sub-9/sub-19).
func benchmarkChannelHierarchicalPresenceCount(b *testing.B, levelCounts []int, includeSubPath bool) {
	ctx := context.Background()
	b.StopTimer()

	allKeyPaths := generateHierarchicalKeyPaths("", levelCounts)
	totalChannels := len(allKeyPaths)

	if totalChannels == 0 {
		b.Skip("No channels generated")
	}

	channels := make([]types.ID, totalChannels)
	ttl := 60 * gotime.Second
	cleanupInterval := 60 * gotime.Second
	pubsub := &mockPubSub{}
	manager := channel.NewManager(pubsub, ttl, cleanupInterval)
	project := types.Project{ID: types.NewID()}

	for i, keyPath := range allKeyPaths {
		clientID, _ := time.ActorIDFromHex(fmt.Sprintf("00000000000000000000000%d", i))
		refkey := types.ChannelRefKey{ProjectID: project.ID, ChannelKey: key.Key(keyPath)}
		channelID, count, err := manager.Attach(ctx, refkey, clientID)
		if err != nil {
			b.Fatalf("Failed to create and attach client %d at path %s: %v", i, keyPath, err)
		}
		channels[i] = channelID
		if count == 0 {
			b.Fatalf("Failed to attach client %d at path %s: %v", i, keyPath, err)
		}
	}

	b.StartTimer()

	// Benchmark: Measure count operation performance
	for i := 0; i < b.N; i++ {
		for _, keyPath := range allKeyPaths {
			for _, mergedKeyPath := range parseAndMergeKeyPath(key.Key(keyPath)) {
				_ = manager.PresenceCount(types.ChannelRefKey{ProjectID: project.ID, ChannelKey: key.Key(mergedKeyPath)}, includeSubPath)
			}
		}
	}

	b.StopTimer()
}

func BenchmarkChannelHierarchicalPresenceCount(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	svr := helper.TestServerWithSnapshotCfg(100_000, 100_000)
	b.Cleanup(func() {
		if err := svr.Shutdown(true); err != nil {
			b.Fatal(err)
		}
	})

	testCases := []struct {
		name           string
		levelCounts    []int
		includeSubPath bool
	}{
		{
			name:           "flat-100 includeSubPath false",
			levelCounts:    []int{100}, // 100 presences at root level
			includeSubPath: false,
		},
		{
			name:           "flat-100 includeSubPath true",
			levelCounts:    []int{100}, // 100 presences at root level
			includeSubPath: true,
		},
		{
			name:           "2-level-1x100=100 includeSubPath false",
			levelCounts:    []int{1, 100}, // 1 root * 100 children = 100 leaf presences
			includeSubPath: false,
		},
		{
			name:           "2-level-1x100=100 includeSubPath true",
			levelCounts:    []int{10, 10}, // 10 roots * 10 children = 100 leaf presences
			includeSubPath: true,
		},
		{
			name:           "2-level-10x10=100 includeSubPath false",
			levelCounts:    []int{10, 10}, // 10 roots * 10 children = 100 leaf presences
			includeSubPath: true,
		},
		{
			name:           "2-level-10x10=100 includeSubPath true",
			levelCounts:    []int{10, 10}, // 10 roots * 10 children = 100 leaf presences
			includeSubPath: true,
		},
		{
			name:           "3-level-1x10x30=300 includeSubPath false",
			levelCounts:    []int{1, 10, 30}, // 1 * 10 * 30 = 300 leaf presences
			includeSubPath: false,
		},
		{
			name:           "3-level-1x10x30=300 includeSubPath true",
			levelCounts:    []int{1, 10, 30}, // 1 * 10 * 30 = 300 leaf presences
			includeSubPath: true,
		},
		{
			name:           "3-level-10x10x10=1000 includeSubPath false",
			levelCounts:    []int{10, 10, 10}, // 10 * 10 * 10 = 1000 leaf presences
			includeSubPath: false,
		},
		{
			name:           "3-level-10x10x10=1000 includeSubPath true",
			levelCounts:    []int{10, 10, 10}, // 10 * 10 * 10 = 1000 leaf presences
			includeSubPath: true,
		},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkChannelHierarchicalPresenceCount(b, tc.levelCounts, tc.includeSubPath)
		})
	}
}

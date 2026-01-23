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
	"sync"
	"sync/atomic"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/channel"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/backend/messaging"
	"github.com/yorkie-team/yorkie/server/logging"
)

type mockPubSub struct {
	mu     sync.Mutex
	events []events.ChannelEvent
}

func (m *mockPubSub) PublishChannel(ctx context.Context, event events.ChannelEvent) {
	m.mu.Lock()
	m.events = append(m.events, event)
	m.mu.Unlock()
}

// generateHierarchicalKeyPaths generates all leaf key paths for a hierarchical structure.
// For example, with levelCounts = [2, 3], it generates:
// sub-0.sub-0, sub-0.sub-1, sub-0.sub-2, sub-1.sub-0, sub-1.sub-1, sub-1.sub-2
func generateHierarchicalKeyPaths(prefix string, levelCounts []int) []string {
	if len(levelCounts) == 0 {
		return []string{prefix}
	}

	var keyPaths []string
	currentLevelCount := levelCounts[0]
	remainingLevels := levelCounts[1:]

	for i := range currentLevelCount {
		var currentPath string
		if prefix == "" {
			currentPath = fmt.Sprintf("sub-%d", i)
		} else {
			currentPath = fmt.Sprintf("%s.sub-%d", prefix, i)
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

// createBenchManager creates a Manager for benchmarking.
func createBenchManager(b *testing.B) (*channel.Manager, types.ID) {
	pubsub := &mockPubSub{}
	broker := messaging.Ensure(nil)
	db, err := memory.New()
	assert.NoError(b, err)
	_, project, err := db.EnsureDefaultUserAndProject(context.Background(), "test-user", "test-password")
	assert.NoError(b, err)
	manager := channel.NewManager(pubsub, 60*gotime.Second, 60*gotime.Second, nil, broker, db)
	return manager, project.ID
}

// BenchmarkChannelConcurrent measures Manager performance under concurrent load.
// Tests mixed read (SessionCount) and write (Attach) operations with configurable ratios.
// Use this to verify thread-safety and measure contention overhead with flat channel structures.
func BenchmarkChannelConcurrent(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	testCases := []struct {
		name         string
		clientCount  int
		channelCount int
		readRatio    int // percentage of read operations (0-100)
	}{
		// Single channel, varying clients
		{"1ch/10clients/80r", 10, 1, 80},
		{"1ch/50clients/80r", 50, 1, 80},
		{"1ch/100clients/80r", 100, 1, 80},
		{"1ch/100clients/50r", 100, 1, 50},
		{"1ch/100clients/100r", 100, 1, 100},

		// Multiple channels
		{"10ch/100clients/80r", 100, 10, 80},
		{"50ch/100clients/80r", 100, 50, 80},
		{"100ch/100clients/80r", 100, 100, 80},

		// High concurrency
		{"10ch/500clients/80r", 500, 10, 80},
		{"50ch/500clients/80r", 500, 50, 80},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkManagerConcurrentOperations(b, tc.clientCount, tc.channelCount, tc.readRatio)
		})
	}
}

func benchmarkManagerConcurrentOperations(b *testing.B, clientCount, channelCount, readRatio int) {
	ctx := context.Background()
	manager, projectID := createBenchManager(b)

	// Pre-create channels with initial sessions
	channelKeys := make([]types.ChannelRefKey, channelCount)
	sessionIDs := make([]types.ID, 0, channelCount)

	for i := range channelCount {
		channelKeys[i] = types.ChannelRefKey{
			ProjectID:  projectID,
			ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
		}
		clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", i))
		sessionID, _, err := manager.Attach(ctx, channelKeys[i], clientID)
		if err != nil {
			b.Fatalf("Failed to attach initial session: %v", err)
		}
		assert.NotEmpty(b, sessionID, "session ID should not be empty")
		sessionIDs = append(sessionIDs, sessionID)
	}

	// Verify setup
	assert.Equal(b, channelCount, manager.Count(projectID), "channel count mismatch after setup")

	b.ResetTimer()

	var attachErrors int64
	for b.Loop() {
		var wg sync.WaitGroup

		for i := range clientCount {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()

				channelKey := channelKeys[idx%channelCount]

				if idx%100 < readRatio {
					// Read operation: SessionCount
					_ = manager.SessionCount(channelKey, false)
				} else {
					// Write operation: Attach
					clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", idx))
					_, _, err := manager.Attach(ctx, channelKey, clientID)
					if err != nil {
						atomic.AddInt64(&attachErrors, 1)
					}
				}
			}(i)
		}

		wg.Wait()
		assert.Equal(b, int64(0), attachErrors, "no attach errors should occur")
	}
}

// BenchmarkChannelHierarchicalConcurrent measures concurrent operations on hierarchical channel keys.
// Tests how trie traversal performs under concurrent access with multi-level key paths (e.g., "room-1.section-2.desk-3").
// Use this to measure performance impact of channel key depth on concurrent operations.
func BenchmarkChannelHierarchicalConcurrent(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	testCases := []struct {
		name        string
		levelCounts []int
		clientCount int
		readRatio   int
	}{
		// 2-level hierarchy
		{"2-level-10x10/100clients/80r", []int{10, 10}, 100, 80},
		{"2-level-10x10/100clients/50r", []int{10, 10}, 100, 50},
		{"2-level-10x10/100clients/20r", []int{10, 10}, 100, 20},

		// 3-level hierarchy
		{"3-level-10x10x10/100clients/80r", []int{10, 10, 10}, 100, 80},
		{"3-level-10x10x10/100clients/50r", []int{10, 10, 10}, 100, 50},
		{"3-level-10x10x10/100clients/20r", []int{10, 10, 10}, 100, 20},
		{"3-level-10x10x10/500clients/80r", []int{10, 10, 10}, 500, 80},
		{"3-level-10x10x10/500clients/50r", []int{10, 10, 10}, 500, 50},
		{"3-level-10x10x10/500clients/20r", []int{10, 10, 10}, 500, 20},

		// Deep hierarchy
		{"4-level-10x10x10x10/500clients/80r", []int{10, 10, 10, 10}, 500, 80},
		{"4-level-10x10x10x10/500clients/50r", []int{10, 10, 10, 10}, 500, 50},
		{"4-level-10x10x10x10/500clients/20r", []int{10, 10, 10, 10}, 500, 20},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkManagerHierarchicalConcurrent(b, tc.levelCounts, tc.clientCount, tc.readRatio)
		})
	}
}

func benchmarkManagerHierarchicalConcurrent(b *testing.B, levelCounts []int, clientCount, readRatio int) {
	ctx := context.Background()
	manager, projectID := createBenchManager(b)

	allKeyPaths := generateHierarchicalKeyPaths("", levelCounts)
	totalChannels := len(allKeyPaths)

	if totalChannels == 0 {
		b.Skip("No channels generated")
	}

	// Pre-create all channels
	channelKeys := make([]types.ChannelRefKey, totalChannels)
	for i, keyPath := range allKeyPaths {
		channelKeys[i] = types.ChannelRefKey{
			ProjectID:  projectID,
			ChannelKey: key.Key(keyPath),
		}
		clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", i))
		sessionID, _, err := manager.Attach(ctx, channelKeys[i], clientID)
		if err != nil {
			b.Fatalf("Failed to attach initial session: %v", err)
		}
		assert.NotEmpty(b, sessionID, "session ID should not be empty")
	}

	// Verify setup
	assert.Equal(b, totalChannels, manager.Count(projectID), "channel count mismatch after setup")

	b.ResetTimer()

	for b.Loop() {
		var wg sync.WaitGroup

		for i := range clientCount {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()

				channelKey := channelKeys[idx%totalChannels]

				if idx%100 < readRatio {
					// Read: SessionCount with includeSubPath
					_ = manager.SessionCount(channelKey, idx%2 == 0)
				} else {
					// Write: Attach
					clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", idx))
					_, _, err := manager.Attach(ctx, channelKey, clientID)
					assert.NoError(b, err, "attach should succeed")
				}
			}(i)
		}

		wg.Wait()
	}
}

// BenchmarkChannelAttachDetach measures Manager-level Attach/Detach performance (no gRPC).
// Tests concurrent session creation and removal at the Manager layer.
// Use this to measure pure Manager overhead without network latency.
func BenchmarkChannelAttachDetach(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	testCases := []struct {
		name        string
		clientCount int
	}{
		{"10clients", 10},
		{"50clients", 50},
		{"100clients", 100},
		{"500clients", 500},
	}

	for _, tc := range testCases {
		b.Run(fmt.Sprintf("attach/%s", tc.name), func(b *testing.B) {
			benchmarkManagerAttach(b, tc.clientCount)
		})

		b.Run(fmt.Sprintf("detach/%s", tc.name), func(b *testing.B) {
			benchmarkManagerDetach(b, tc.clientCount)
		})

		b.Run(fmt.Sprintf("attach-detach-cycle/%s", tc.name), func(b *testing.B) {
			benchmarkManagerAttachDetachCycle(b, tc.clientCount)
		})
	}
}

func benchmarkManagerAttach(b *testing.B, clientCount int) {
	ctx := context.Background()
	// Create manager once outside the loop
	manager, projectID := createBenchManager(b)

	b.ResetTimer()

	for i := range b.N {
		// Use different channel key per iteration to avoid session accumulation
		channelKey := types.ChannelRefKey{
			ProjectID:  projectID,
			ChannelKey: key.Key(fmt.Sprintf("bench-room-%d", i)),
		}

		var wg sync.WaitGroup

		for j := range clientCount {
			wg.Add(1)
			go func(iterIdx, clientIdx int) {
				defer wg.Done()
				clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%012d%012d", iterIdx, clientIdx))
				_, _, err := manager.Attach(ctx, channelKey, clientID)
				assert.NoError(b, err, "attach should succeed")
			}(i, j)
		}

		wg.Wait()
	}
}

func benchmarkManagerDetach(b *testing.B, clientCount int) {
	ctx := context.Background()
	// Create manager once outside the loop
	manager, projectID := createBenchManager(b)

	b.ResetTimer()

	for i := range b.N {
		b.StopTimer()

		// Use different channel key per iteration
		channelKey := types.ChannelRefKey{
			ProjectID:  projectID,
			ChannelKey: key.Key(fmt.Sprintf("bench-room-%d", i)),
		}

		// Pre-attach clients
		sessionIDs := make([]types.ID, clientCount)
		for j := range clientCount {
			clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%012d%012d", i, j))
			sessionID, _, err := manager.Attach(ctx, channelKey, clientID)
			assert.NoError(b, err, "attach should succeed during setup")
			assert.NotEmpty(b, sessionID, "session ID should not be empty")
			sessionIDs[j] = sessionID
		}

		// Verify pre-attach count
		assert.Equal(b, int64(clientCount), manager.SessionCount(channelKey, false), "session count mismatch before detach")

		b.StartTimer()

		// Concurrent detach
		var wg sync.WaitGroup
		for j := range clientCount {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				_, _ = manager.Detach(ctx, sessionIDs[idx])
			}(j)
		}

		wg.Wait()

		b.StopTimer()

		// Verify all sessions detached
		assert.Equal(b, int64(0), manager.SessionCount(channelKey, false), "all sessions should be detached")

		b.StartTimer()
	}
}

func benchmarkManagerAttachDetachCycle(b *testing.B, clientCount int) {
	ctx := context.Background()
	// Create manager once - attach-detach cycle leaves channel empty, so no accumulation
	manager, projectID := createBenchManager(b)
	channelKey := types.ChannelRefKey{
		ProjectID:  projectID,
		ChannelKey: key.Key("bench-room"),
	}

	b.ResetTimer()

	for range b.N {

		var wg sync.WaitGroup
		var attachErrors int64

		// Concurrent attach
		sessionIDs := make([]types.ID, clientCount)
		var mu sync.Mutex

		for j := range clientCount {
			wg.Add(1)
			go func(clientIdx int) {
				defer wg.Done()
				clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", clientIdx))
				sessionID, _, err := manager.Attach(ctx, channelKey, clientID)
				if err != nil {
					atomic.AddInt64(&attachErrors, 1)
				}
				mu.Lock()
				sessionIDs[clientIdx] = sessionID
				mu.Unlock()
			}(j)
		}

		wg.Wait()

		b.StopTimer()
		assert.Equal(b, int64(0), attachErrors, "all attaches should succeed")
		assert.Equal(b, int64(clientCount), manager.SessionCount(channelKey, false), "session count should match client count")
		b.StartTimer()

		// Concurrent detach
		for j := range clientCount {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				mu.Lock()
				sessionID := sessionIDs[idx]
				mu.Unlock()
				_, _ = manager.Detach(ctx, sessionID)
			}(j)
		}

		wg.Wait()

		b.StopTimer()
		assert.Equal(b, int64(0), manager.SessionCount(channelKey, false), "all sessions should be detached")
		b.StartTimer()
	}
}

// BenchmarkChannelSessionCount measures SessionCount query performance.
// Tests session counting with various hierarchical structures and includeSubPath option.
// Use this to measure trie traversal cost when counting sessions in hierarchical channels.
func BenchmarkChannelSessionCount(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	testCases := []struct {
		name           string
		levelCounts    []int
		includeSubPath bool
	}{
		// Flat structures
		{"flat-100/includeSubPath=false", []int{100}, false},
		{"flat-100/includeSubPath=true", []int{100}, true},
		{"flat-1000/includeSubPath=false", []int{1000}, false},
		{"flat-1000/includeSubPath=true", []int{1000}, true},

		// 2-level structures
		{"2-level-10x10/includeSubPath=false", []int{10, 10}, false},
		{"2-level-10x10/includeSubPath=true", []int{10, 10}, true},
		{"2-level-10x100/includeSubPath=false", []int{10, 100}, false},
		{"2-level-10x100/includeSubPath=true", []int{10, 100}, true},

		// 3-level structures
		{"3-level-10x10x10/includeSubPath=false", []int{10, 10, 10}, false},
		{"3-level-10x10x10/includeSubPath=true", []int{10, 10, 10}, true},
		{"3-level-5x10x20/includeSubPath=false", []int{5, 10, 20}, false},
		{"3-level-5x10x20/includeSubPath=true", []int{5, 10, 20}, true},

		// 4-level structures
		{"4-level-5x5x5x8/includeSubPath=false", []int{5, 5, 5, 8}, false},
		{"4-level-5x5x5x8/includeSubPath=true", []int{5, 5, 5, 8}, true},

		// Deep structures
		{"deep-4x4x4x4x2x2/includeSubPath=false", []int{4, 4, 4, 4, 2, 2}, false},
		{"deep-4x4x4x4x2x2/includeSubPath=true", []int{4, 4, 4, 4, 2, 2}, true},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkManagerSessionCount(b, tc.levelCounts, tc.includeSubPath)
		})
	}
}

func benchmarkManagerSessionCount(b *testing.B, levelCounts []int, includeSubPath bool) {
	ctx := context.Background()
	manager, projectID := createBenchManager(b)

	allKeyPaths := generateHierarchicalKeyPaths("", levelCounts)
	totalChannels := len(allKeyPaths)

	if totalChannels == 0 {
		b.Skip("No channels generated")
	}

	// Pre-create all channels with sessions
	channelKeys := make([]types.ChannelRefKey, totalChannels)
	for i, keyPath := range allKeyPaths {
		channelKeys[i] = types.ChannelRefKey{
			ProjectID:  projectID,
			ChannelKey: key.Key(keyPath),
		}
		clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", i))
		sessionID, _, err := manager.Attach(ctx, channelKeys[i], clientID)
		if err != nil {
			b.Fatalf("Failed to attach: %v", err)
		}
		assert.NotEmpty(b, sessionID, "session ID should not be empty")
	}

	// Verify setup
	assert.Equal(b, totalChannels, manager.Count(projectID), "channel count mismatch after setup")

	b.ResetTimer()

	for b.Loop() {
		for _, channelKey := range channelKeys {
			count := manager.SessionCount(channelKey, includeSubPath)
			// Each channel has at least 1 session
			assert.GreaterOrEqual(b, count, int64(1), "session count should be at least 1")
		}
	}
}

// BenchmarkChannelList measures channel listing performance with flat channel keys.
// Tests prefix filtering and pagination with various channel counts.
// Use this to measure List API performance for simple channel structures.
func BenchmarkChannelList(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	testCases := []struct {
		name         string
		channelCount int
		queryPrefix  string
		limit        int
	}{
		// No query prefix (list all)
		{"100ch_no_prefix_limit10", 100, "", 10},
		{"100ch_no_prefix_limit50", 100, "", 50},
		{"100ch_no_prefix_limit100", 100, "", 100},
		{"500ch_no_prefix_limit10", 500, "", 10},
		{"500ch_no_prefix_limit50", 500, "", 50},
		{"500ch_no_prefix_limit100", 500, "", 100},

		// With query prefix
		{"100ch_prefix_room-1_limit10", 100, "room-1", 10},
		{"100ch_prefix_room-1_limit50", 100, "room-1", 50},
		{"100ch_prefix_room-1_limit100", 100, "room-1", 100},
		{"500ch_prefix_room-1_limit10", 500, "room-1", 10},
		{"500ch_prefix_room-1_limit50", 500, "room-1", 50},
		{"500ch_prefix_room-1_limit100", 500, "room-1", 100},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkManagerList(b, tc.channelCount, tc.queryPrefix, tc.limit)
		})
	}
}

func benchmarkManagerList(b *testing.B, channelCount int, queryPrefix string, limit int) {
	ctx := context.Background()
	manager, projectID := createBenchManager(b)

	// Create channels
	for i := range channelCount {
		channelKey := types.ChannelRefKey{
			ProjectID:  projectID,
			ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
		}
		clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", i))
		sessionID, _, err := manager.Attach(ctx, channelKey, clientID)
		if err != nil {
			b.Fatalf("Failed to attach: %v", err)
		}
		assert.NotEmpty(b, sessionID, "session ID should not be empty")
	}

	// Verify setup
	assert.Equal(b, channelCount, manager.Count(projectID), "channel count mismatch after setup")

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		results := manager.List(projectID, queryPrefix, limit)
		assert.NotNil(b, results, "list results should not be nil")
		assert.LessOrEqual(b, len(results), limit, "list results should respect limit")
	}
}

// BenchmarkChannelListHierarchical measures List performance with hierarchical channel keys.
// Tests prefix filtering on multi-level key paths (e.g., "room-1.section-2").
// Use this to measure List API performance for nested channel structures.
func BenchmarkChannelListHierarchical(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	testCases := []struct {
		name        string
		levelCounts []int
		queryPrefix string
		limit       int
	}{
		// 2-level hierarchical
		{"2-level-10x100_no_prefix_limit50", []int{10, 100}, "", 50},
		{"2-level-10x100_prefix_sub-0_limit50", []int{10, 10}, "sub-0", 50},

		// 3-level hierarchical
		{"3-level-10x10x10_no_prefix_limit100", []int{10, 10, 10}, "", 100},
		{"3-level-10x10x10_prefix_sub-0_limit100", []int{10, 10, 10}, "sub-0", 100},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkManagerListHierarchical(b, tc.levelCounts, tc.queryPrefix, tc.limit)
		})
	}
}

func benchmarkManagerListHierarchical(b *testing.B, levelCounts []int, queryPrefix string, limit int) {
	ctx := context.Background()
	manager, projectID := createBenchManager(b)

	allKeyPaths := generateHierarchicalKeyPaths("", levelCounts)
	if len(allKeyPaths) == 0 {
		b.Skip("No channels generated")
	}

	// Create channels
	for i, keyPath := range allKeyPaths {
		channelKey := types.ChannelRefKey{
			ProjectID:  projectID,
			ChannelKey: key.Key(keyPath),
		}
		clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", i))
		sessionID, _, err := manager.Attach(ctx, channelKey, clientID)
		if err != nil {
			b.Fatalf("Failed to attach: %v", err)
		}
		assert.NotEmpty(b, sessionID, "session ID should not be empty")
	}

	// Verify setup
	assert.Equal(b, len(allKeyPaths), manager.Count(projectID), "channel count mismatch after setup")

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		results := manager.List(projectID, queryPrefix, limit)
		assert.NotNil(b, results, "list results should not be nil")
		assert.LessOrEqual(b, len(results), limit, "list results should respect limit")
	}
}

// BenchmarkChannelCleanupExpired measures expired session cleanup performance.
// Tests how efficiently the manager removes stale sessions across many channels.
// Use this to validate cleanup scalability under various session/channel configurations.
func BenchmarkChannelCleanupExpired(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	testCases := []struct {
		name          string
		channelCount  int
		sessionsPerCh int
	}{
		// Single session per channel
		{"100ch * 1 session_expired", 100, 1},
		{"500ch * 1 session_expired", 500, 1},
		{"1000ch * 1 session_expired", 1000, 1},

		// Multiple sessions per channel
		{"100ch * 5 sessions_expired", 100, 5},
		{"100ch * 10 sessions_expired", 100, 10},
		{"100ch * 20 sessions_expired", 100, 20},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkManagerCleanupExpired(b, tc.channelCount, tc.sessionsPerCh)
		})
	}
}

func benchmarkManagerCleanupExpired(b *testing.B, channelCount int, sessionsPerCh int) {
	ctx := context.Background()

	// Create manager once with very short TTL
	pubsub := &mockPubSub{}
	broker := messaging.Ensure(nil)
	db, err := memory.New()
	assert.NoError(b, err)
	_, project, err := db.EnsureDefaultUserAndProject(context.Background(), "test-user", "test-password")
	assert.NoError(b, err)

	// Use 1ms TTL so sessions expire immediately
	manager := channel.NewManager(pubsub, 1*gotime.Millisecond, 60*gotime.Second, nil, broker, db)

	b.ResetTimer()

	iteration := 0
	for b.Loop() {
		b.StopTimer()

		// Attach sessions with unique client IDs per iteration
		for c := range channelCount {
			channelKey := types.ChannelRefKey{
				ProjectID:  project.ID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", c)),
			}

			for s := range sessionsPerCh {
				// Use iteration index to create unique client IDs
				clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%08d%08d%08d", iteration, c, s))
				_, _, err := manager.Attach(ctx, channelKey, clientID)
				if err != nil {
					b.Fatalf("Failed to attach: %v", err)
				}
			}
		}

		b.StartTimer()

		// Measure cleanup performance
		_, err = manager.CleanupExpired(ctx)
		if err != nil {
			b.Fatalf("CleanupExpired failed: %v", err)
		}

		iteration++
	}
}

// BenchmarkChannelCount measures channel counting performance per project.
// Tests how trie size affects Count operation latency.
// Use this to validate O(n) counting performance at scale.
func BenchmarkChannelCount(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	testCases := []struct {
		name         string
		channelCount int
	}{
		{"100_channels", 100},
		{"500_channels", 500},
		{"1000_channels", 1000},
		{"5000_channels", 5000},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			manager, projectID := createBenchManager(b)

			// Create channels
			for i := range tc.channelCount {
				channelKey := types.ChannelRefKey{
					ProjectID:  projectID,
					ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
				}
				clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", i))
				sessionID, _, err := manager.Attach(ctx, channelKey, clientID)
				assert.NoError(b, err, "attach should succeed")
				assert.NotEmpty(b, sessionID, "session ID should not be empty")
			}

			// Verify setup
			assert.Equal(b, tc.channelCount, manager.Count(projectID), "channel count mismatch after setup")

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				count := manager.Count(projectID)
				assert.Equal(b, tc.channelCount, count, "count should match expected")
			}
		})
	}
}

// BenchmarkChannelStats measures global statistics gathering performance.
// Tests full trie traversal to compute channel and session counts.
// Use this to validate Stats API performance at scale.
func BenchmarkChannelStats(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	testCases := []struct {
		name          string
		channelCount  int
		sessionsPerCh int
	}{
		{"100ch_1session", 100, 1},
		{"100ch_10session", 100, 10},
		{"500ch_1session", 500, 1},
		{"500ch_10session", 500, 10},
		{"1000ch_1session", 1000, 1},
		{"1000ch_10session", 1000, 10},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			manager, projectID := createBenchManager(b)

			// Create channels with sessions
			for c := range tc.channelCount {
				channelKey := types.ChannelRefKey{
					ProjectID:  projectID,
					ChannelKey: key.Key(fmt.Sprintf("room-%d", c)),
				}
				for s := range tc.sessionsPerCh {
					clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%012d%012d", c, s))
					sessionID, _, err := manager.Attach(ctx, channelKey, clientID)
					assert.NoError(b, err, "attach should succeed")
					assert.NotEmpty(b, sessionID, "session ID should not be empty")
				}
			}

			// Verify setup
			expectedSessions := tc.channelCount * tc.sessionsPerCh
			stats := manager.Stats()
			assert.Equal(b, tc.channelCount, stats["total_channels"], "channel count mismatch after setup")
			assert.Equal(b, expectedSessions, stats["total_sessions"], "session count mismatch after setup")

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				stats := manager.Stats()
				assert.Equal(b, tc.channelCount, stats["total_channels"], "channel count should be consistent")
				assert.GreaterOrEqual(b, stats["total_sessions"], expectedSessions, "session count should be at least expected")
			}
		})
	}
}

// BenchmarkChannel_Memory benchmarks memory allocation patterns.
// Measures memory allocations for inserting channels and sessions.
func BenchmarkChannel_Memory(b *testing.B) {
	testCases := []struct {
		name               string
		channelCount       int
		sessionsPerChannel int
	}{
		{"500_channels * 10 sessions", 500, 10},
		{"1000_channels * 10 sessions", 1000, 10},
		{"1000_channels * 20 sessions", 1000, 20},
		{"2000_channels * 10 sessions", 2000, 10},
		{"2000_channels * 20 sessions", 2000, 20},
	}

	for _, tc := range testCases {
		b.Run(fmt.Sprintf("insert/%s", tc.name), func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				b.StopTimer()
				manager, projectID := createBenchManager(b)
				b.StartTimer()

				for c := range tc.channelCount {
					channelKey := types.ChannelRefKey{
						ProjectID:  projectID,
						ChannelKey: key.Key(fmt.Sprintf("room-%d", c)),
					}
					for s := range tc.sessionsPerChannel {
						clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%012d%012d", c, s))
						_, _, err := manager.Attach(ctx, channelKey, clientID)
						assert.NoError(b, err, "attach should succeed")
					}
				}
			}
		})
	}
}

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
)

// concurrencyMockPubSub is a thread-safe mock PubSub for concurrency tests.
type concurrencyMockPubSub struct {
	eventCount int64
}

func (m *concurrencyMockPubSub) PublishChannel(ctx context.Context, event events.ChannelEvent) {
	atomic.AddInt64(&m.eventCount, 1)
}

func (m *concurrencyMockPubSub) getEventCount() int64 {
	return atomic.LoadInt64(&m.eventCount)
}

// createConcurrencyTestManager creates a Manager for concurrency testing.
func createConcurrencyTestManager(t testing.TB) (*channel.Manager, types.ID, *concurrencyMockPubSub) {
	pubsub := &concurrencyMockPubSub{}
	broker := messaging.Ensure(nil)
	db, err := memory.New()
	assert.NoError(t, err)
	_, project, err := db.EnsureDefaultUserAndProject(context.Background(), "test-user", "test-password")
	assert.NoError(t, err)
	manager := channel.NewManager(pubsub, 60*gotime.Second, 60*gotime.Second, nil, broker, db)
	return manager, project.ID, pubsub
}

// BenchmarkChannelConcurrency_AttachSameChannel benchmarks concurrent Attach to the same channel.
// This tests the atomic GetOrInsert behavior and session map thread-safety.
func BenchmarkChannelConcurrency_AttachSameChannel(b *testing.B) {
	testCases := []struct {
		name           string
		goroutineCount int
	}{
		{"10_goroutines", 10},
		{"50_goroutines", 50},
		{"100_goroutines", 100},
		{"500_goroutines", 500},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				// Create fresh manager for each iteration to avoid accumulation
				manager, projectID, _ := createConcurrencyTestManager(b)
				channelKey := types.ChannelRefKey{
					ProjectID:  projectID,
					ChannelKey: "hot-channel",
				}

				var wg sync.WaitGroup
				var successCount int64

				for g := range tc.goroutineCount {
					wg.Add(1)
					go func(gIdx int) {
						defer wg.Done()
						clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%012d%012d", i, gIdx))
						_, _, err := manager.Attach(ctx, channelKey, clientID)
						if err == nil {
							atomic.AddInt64(&successCount, 1)
						}
					}(g)
				}

				wg.Wait()

				// Verify all attaches succeeded
				if successCount != int64(tc.goroutineCount) {
					b.Fatalf("Expected %d successful attaches, got %d", tc.goroutineCount, successCount)
				}

				// Verify session count matches
				count := manager.SessionCount(channelKey, false)
				if count != int64(tc.goroutineCount) {
					b.Fatalf("Expected session count %d, got %d", tc.goroutineCount, count)
				}
			}
		})
	}
}

// BenchmarkChannelConcurrency_AttachDifferentChannels benchmarks concurrent Attach to different channels.
// Tests channel creation concurrency when multiple goroutines create separate channels simultaneously.
func BenchmarkChannelConcurrency_AttachDifferentChannels(b *testing.B) {
	testCases := []struct {
		name           string
		goroutineCount int
	}{
		{"10_goroutines", 10},
		{"50_goroutines", 50},
		{"100_goroutines", 100},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				// Create fresh manager per iteration to avoid channel accumulation
				manager, projectID, _ := createConcurrencyTestManager(b)
				var wg sync.WaitGroup
				var successCount int64

				for g := range tc.goroutineCount {
					wg.Add(1)
					go func(gIdx int) {
						defer wg.Done()
						channelKey := types.ChannelRefKey{
							ProjectID:  projectID,
							ChannelKey: key.Key(fmt.Sprintf("room-%d", gIdx)),
						}
						clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", gIdx))
						sessionID, _, err := manager.Attach(ctx, channelKey, clientID)
						if err != nil {
							b.Errorf("Attach failed: %v", err)
						}
						if sessionID != "" {
							atomic.AddInt64(&successCount, 1)
						}
					}(g)
				}

				wg.Wait()

				// Verify all channels were created
				assert.Equal(b, int64(tc.goroutineCount), successCount, "all attaches should succeed")
				assert.Equal(b, tc.goroutineCount, manager.Count(projectID), "channel count should match goroutine count")
			}
		})
	}
}

// BenchmarkChannelConcurrency_AttachDetachMixed benchmarks concurrent Attach and Detach operations.
// This tests the race between session creation and deletion.
func BenchmarkChannelConcurrency_AttachDetachMixed(b *testing.B) {
	testCases := []struct {
		name           string
		goroutineCount int
		detachRatio    int // percentage of detach operations
	}{
		{"50g_20pct_detach", 50, 20},
		{"50g_50pct_detach", 50, 50},
		{"100g_20pct_detach", 100, 20},
		{"100g_50pct_detach", 100, 50},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				// Create fresh manager per iteration to avoid session accumulation
				manager, projectID, _ := createConcurrencyTestManager(b)

				channelKey := types.ChannelRefKey{
					ProjectID:  projectID,
					ChannelKey: "mixed-channel",
				}

				// Pre-attach some sessions for detach operations
				preAttachCount := tc.goroutineCount
				sessionIDs := make([]types.ID, preAttachCount)
				for j := range preAttachCount {
					clientID, _ := time.ActorIDFromHex(fmt.Sprintf("aaa%021d", j))
					sessionID, _, err := manager.Attach(ctx, channelKey, clientID)
					assert.NoError(b, err, "pre-attach should succeed")
					assert.NotEmpty(b, sessionID, "session ID should not be empty")
					sessionIDs[j] = sessionID
				}

				// Verify pre-attach
				assert.Equal(b, int64(preAttachCount), manager.SessionCount(channelKey, false), "pre-attach count mismatch")

				var wg sync.WaitGroup
				var sessionIdx int32
				var attachCount, detachCount int64

				for g := range tc.goroutineCount {
					wg.Add(1)
					go func(gIdx int) {
						defer wg.Done()

						if gIdx%100 < tc.detachRatio {
							// Detach operation
							idx := atomic.AddInt32(&sessionIdx, 1) - 1
							if int(idx) < len(sessionIDs) {
								_, _ = manager.Detach(ctx, sessionIDs[idx])
								atomic.AddInt64(&detachCount, 1)
							}
						} else {
							// Attach operation
							clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", gIdx))
							_, _, err := manager.Attach(ctx, channelKey, clientID)
							assert.NoError(b, err, "attach should succeed")
							atomic.AddInt64(&attachCount, 1)
						}
					}(g)
				}

				wg.Wait()

				// Verify operations happened
				assert.Greater(b, attachCount+detachCount, int64(0), "some operations should have occurred")
			}
		})
	}
}

// BenchmarkChannelConcurrency_SessionCountWhileModifying benchmarks SessionCount during concurrent modifications.
// This tests read consistency during writes.
func BenchmarkChannelConcurrency_SessionCountWhileModifying(b *testing.B) {
	testCases := []struct {
		name         string
		readers      int
		writers      int
		channelCount int
	}{
		{"10r_10w_10ch", 10, 10, 10},
		{"50r_10w_10ch", 50, 10, 10},
		{"10r_50w_10ch", 10, 50, 10},
		{"50r_50w_50ch", 50, 50, 50},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				// Create fresh manager per iteration to avoid session accumulation
				manager, projectID, _ := createConcurrencyTestManager(b)

				// Create channels
				channelKeys := make([]types.ChannelRefKey, tc.channelCount)
				for j := range tc.channelCount {
					channelKeys[j] = types.ChannelRefKey{
						ProjectID:  projectID,
						ChannelKey: key.Key(fmt.Sprintf("room-%d", j)),
					}
					// Pre-attach one session
					clientID, _ := time.ActorIDFromHex(fmt.Sprintf("init%022d", j))
					sessionID, _, err := manager.Attach(ctx, channelKeys[j], clientID)
					assert.NoError(b, err, "pre-attach should succeed")
					assert.NotEmpty(b, sessionID, "session ID should not be empty")
				}

				// Verify setup
				assert.Equal(b, tc.channelCount, manager.Count(projectID), "channel count mismatch after setup")

				var wg sync.WaitGroup
				done := make(chan struct{})
				var readCount int64

				// Start readers
				for r := range tc.readers {
					wg.Add(1)
					go func(rIdx int) {
						defer wg.Done()
						for {
							select {
							case <-done:
								return
							default:
								channelKey := channelKeys[rIdx%tc.channelCount]
								count := manager.SessionCount(channelKey, rIdx%2 == 0)
								// Session count should always be >= 1 (initial session)
								if count >= 1 {
									atomic.AddInt64(&readCount, 1)
								}
							}
						}
					}(r)
				}

				// Start writers
				var writeCount int32
				for w := range tc.writers {
					wg.Add(1)
					go func(wIdx int) {
						defer wg.Done()
						channelKey := channelKeys[wIdx%tc.channelCount]
						clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", wIdx))
						_, _, err := manager.Attach(ctx, channelKey, clientID)
						assert.NoError(b, err, "attach should succeed")
						atomic.AddInt32(&writeCount, 1)

						if atomic.LoadInt32(&writeCount) >= int32(tc.writers) {
							close(done)
						}
					}(w)
				}

				wg.Wait()

				// Verify reads and writes happened
				assert.Greater(b, readCount, int64(0), "some reads should have occurred")
				assert.Equal(b, int32(tc.writers), writeCount, "all writes should have completed")
			}
		})
	}
}

// BenchmarkChannelConcurrency_ListWhileModifying benchmarks List during concurrent modifications.
// Tests read consistency of List operation while channels are being created concurrently.
func BenchmarkChannelConcurrency_ListWhileModifying(b *testing.B) {
	testCases := []struct {
		name    string
		readers int
		writers int
	}{
		{"10r_10w", 10, 10},
		{"50r_10w", 50, 10},
		{"10r_50w", 10, 50},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				// Create fresh manager per iteration to avoid channel accumulation
				manager, projectID, _ := createConcurrencyTestManager(b)

				// Pre-create some channels
				preCreateCount := 100
				for j := range preCreateCount {
					channelKey := types.ChannelRefKey{
						ProjectID:  projectID,
						ChannelKey: key.Key(fmt.Sprintf("room-%d", j)),
					}
					clientID, _ := time.ActorIDFromHex(fmt.Sprintf("init%022d", j))
					sessionID, _, err := manager.Attach(ctx, channelKey, clientID)
					assert.NoError(b, err, "pre-create should succeed")
					assert.NotEmpty(b, sessionID, "session ID should not be empty")
				}

				// Verify setup
				assert.Equal(b, preCreateCount, manager.Count(projectID), "channel count mismatch after setup")

				var wg sync.WaitGroup
				done := make(chan struct{})
				var listCount int64

				// Start readers (List operations)
				for r := range tc.readers {
					wg.Add(1)
					go func(rIdx int) {
						defer wg.Done()
						for {
							select {
							case <-done:
								return
							default:
								results := manager.List(projectID, "", 50)
								// List should return results (we have pre-created channels)
								if len(results) > 0 {
									atomic.AddInt64(&listCount, 1)
								}
							}
						}
					}(r)
				}

				// Start writers
				var writeCount int32
				for w := range tc.writers {
					wg.Add(1)
					go func(wIdx int) {
						defer wg.Done()
						channelKey := types.ChannelRefKey{
							ProjectID:  projectID,
							ChannelKey: key.Key(fmt.Sprintf("new-room-%d", wIdx)),
						}
						clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", wIdx))
						_, _, err := manager.Attach(ctx, channelKey, clientID)
						assert.NoError(b, err, "attach should succeed")
						atomic.AddInt32(&writeCount, 1)

						if atomic.LoadInt32(&writeCount) >= int32(tc.writers) {
							close(done)
						}
					}(w)
				}

				wg.Wait()

				// Verify operations
				assert.Greater(b, listCount, int64(0), "some list operations should have occurred")
				assert.Equal(b, int32(tc.writers), writeCount, "all writes should have completed")
				assert.GreaterOrEqual(b, manager.Count(projectID), preCreateCount+tc.writers, "channel count should include new channels")
			}
		})
	}
}

// BenchmarkChannelConcurrency_ChannelManagerContention benchmarks Manager under high contention.
// Tests concurrent Attach operations with same/different channels to measure lock contention.
func BenchmarkChannelConcurrency_ChannelManagerContention(b *testing.B) {
	testCases := []struct {
		name       string
		goroutines int
		operation  string // "same_key", "different_keys", "mixed"
	}{
		{"same_key_100g", 100, "same_key"},
		{"different_keys_100g", 100, "different_keys"},
		{"mixed_100g", 100, "mixed"},
		{"same_key_500g", 500, "same_key"},
		{"different_keys_500g", 500, "different_keys"},
		{"mixed_500g", 500, "mixed"},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			manager, projectID, _ := createConcurrencyTestManager(b)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				var wg sync.WaitGroup
				sessionIDs := make([]types.ID, tc.goroutines)
				var mu sync.Mutex
				var successCount int64

				for g := range tc.goroutines {
					wg.Add(1)
					go func(iterIdx, gIdx int) {
						defer wg.Done()

						var channelKey types.ChannelRefKey
						switch tc.operation {
						case "same_key":
							channelKey = types.ChannelRefKey{
								ProjectID:  projectID,
								ChannelKey: "hot-spot",
							}
						case "different_keys":
							channelKey = types.ChannelRefKey{
								ProjectID:  projectID,
								ChannelKey: key.Key(fmt.Sprintf("room-%d", gIdx)),
							}
						case "mixed":
							if gIdx%2 == 0 {
								channelKey = types.ChannelRefKey{
									ProjectID:  projectID,
									ChannelKey: "hot-spot",
								}
							} else {
								channelKey = types.ChannelRefKey{
									ProjectID:  projectID,
									ChannelKey: key.Key(fmt.Sprintf("room-%d", gIdx)),
								}
							}
						}

						clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%012d%012d", iterIdx, gIdx))
						sessionID, _, err := manager.Attach(ctx, channelKey, clientID)
						if err == nil && sessionID != "" {
							atomic.AddInt64(&successCount, 1)
						}
						mu.Lock()
						sessionIDs[gIdx] = sessionID
						mu.Unlock()
					}(i, g)
				}

				wg.Wait()

				// Verify all attaches succeeded
				assert.Equal(b, int64(tc.goroutines), successCount, "all attaches should succeed")

				// Detach all sessions to clean up for next iteration
				var detachCount int64
				for _, sessionID := range sessionIDs {
					if sessionID != "" {
						_, err := manager.Detach(ctx, sessionID)
						if err == nil {
							atomic.AddInt64(&detachCount, 1)
						}
					}
				}

				// Verify cleanup
				assert.Equal(b, int64(tc.goroutines), detachCount, "all detaches should succeed")
			}
		})
	}
}

// BenchmarkChannelConcurrency_StressTest performs a comprehensive stress test with all operations.
// Tests mixed workload of Attach, SessionCount, List, Count, and Stats operations under high concurrency.
func BenchmarkChannelConcurrency_StressTest(b *testing.B) {
	testCases := []struct {
		name           string
		goroutines     int
		channelCount   int
		operationsEach int
	}{
		{"50g_100ch_10ops", 50, 100, 10},
		{"100g_100ch_10ops", 100, 100, 10},
		{"100g_500ch_10ops", 100, 500, 10},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			manager, projectID, pubsub := createConcurrencyTestManager(b)

			// Pre-create channels
			channelKeys := make([]types.ChannelRefKey, tc.channelCount)
			sessionIDs := make([]types.ID, tc.channelCount)
			for i := range tc.channelCount {
				channelKeys[i] = types.ChannelRefKey{
					ProjectID:  projectID,
					ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
				}
				clientID, _ := time.ActorIDFromHex(fmt.Sprintf("init%022d", i))
				sessionID, _, err := manager.Attach(ctx, channelKeys[i], clientID)
				assert.NoError(b, err, "pre-attach should succeed")
				assert.NotEmpty(b, sessionID, "session ID should not be empty")
				sessionIDs[i] = sessionID
			}

			// Verify setup
			assert.Equal(b, tc.channelCount, manager.Count(projectID), "channel count mismatch after setup")

			initialEventCount := pubsub.getEventCount()

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				var wg sync.WaitGroup
				var errorCount int64
				var attachCount, readCount int64

				for g := range tc.goroutines {
					wg.Add(1)
					go func(gIdx int) {
						defer wg.Done()

						for op := range tc.operationsEach {
							channelKey := channelKeys[(gIdx+op)%tc.channelCount]

							switch (gIdx + op) % 6 {
							case 0:
								// Attach
								clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%08d%08d%08d", i, gIdx, op))
								_, _, err := manager.Attach(ctx, channelKey, clientID)
								if err != nil {
									atomic.AddInt64(&errorCount, 1)
								} else {
									atomic.AddInt64(&attachCount, 1)
								}
							case 1:
								// SessionCount (direct)
								count := manager.SessionCount(channelKey, false)
								if count >= 1 {
									atomic.AddInt64(&readCount, 1)
								}
							case 2:
								// SessionCount (with subpath)
								count := manager.SessionCount(channelKey, true)
								if count >= 1 {
									atomic.AddInt64(&readCount, 1)
								}
							case 3:
								// List
								results := manager.List(projectID, "", 50)
								if len(results) > 0 {
									atomic.AddInt64(&readCount, 1)
								}
							case 4:
								// Count
								count := manager.Count(projectID)
								if count >= tc.channelCount {
									atomic.AddInt64(&readCount, 1)
								}
							case 5:
								// Stats
								stats := manager.Stats()
								if stats["total_channels"] >= tc.channelCount {
									atomic.AddInt64(&readCount, 1)
								}
							}
						}
					}(g)
				}

				wg.Wait()

				// Verify operations
				assert.Equal(b, int64(0), errorCount, "no errors should occur")
				assert.Greater(b, attachCount, int64(0), "some attaches should have occurred")
				assert.Greater(b, readCount, int64(0), "some reads should have occurred")
			}

			b.StopTimer()

			// Verify event count increased (system is working)
			finalEventCount := pubsub.getEventCount()
			assert.Greater(b, finalEventCount, initialEventCount, "events should have been published")
		})
	}
}

// BenchmarkChannelConcurrency_DataRaceDetection is specifically designed to trigger data races if they exist.
// Run with: go test -race -bench=DataRace
func BenchmarkChannelConcurrency_DataRaceDetection(b *testing.B) {
	ctx := context.Background()
	manager, projectID, _ := createConcurrencyTestManager(b)

	channelKey := types.ChannelRefKey{
		ProjectID:  projectID,
		ChannelKey: "race-test-channel",
	}

	// Pre-attach
	clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", 0))
	sessionID, _, err := manager.Attach(ctx, channelKey, clientID)
	assert.NoError(b, err, "pre-attach should succeed")
	assert.NotEmpty(b, sessionID, "session ID should not be empty")

	// Verify setup
	assert.Equal(b, int64(1), manager.SessionCount(channelKey, false), "initial session count should be 1")

	b.ResetTimer()

	var totalOps int64
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 10 {
			case 0, 1, 2:
				// Attach (30%)
				cID, _ := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1000))
				_, _, _ = manager.Attach(ctx, channelKey, cID)
			case 3:
				// Detach (10%)
				_, _ = manager.Detach(ctx, sessionID)
			case 4, 5:
				// SessionCount (20%)
				count := manager.SessionCount(channelKey, i%2 == 0)
				// Count should be non-negative
				assert.GreaterOrEqual(b, count, int64(0), "session count should be non-negative")
			case 6:
				// List (10%)
				results := manager.List(projectID, "", 50)
				assert.NotNil(b, results, "list results should not be nil")
			case 7:
				// Count (10%)
				count := manager.Count(projectID)
				assert.GreaterOrEqual(b, count, 0, "count should be non-negative")
			case 8:
				// Stats (10%)
				stats := manager.Stats()
				assert.NotNil(b, stats, "stats should not be nil")
			case 9:
				// Refresh (10%)
				_ = manager.Refresh(ctx, sessionID)
			}
			atomic.AddInt64(&totalOps, 1)
			i++
		}
	})

	b.StopTimer()

	// Verify some operations happened
	assert.Greater(b, totalOps, int64(0), "some operations should have been executed")
}

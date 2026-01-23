//go:build bench

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

type stressMockPubSub struct{}

func (m *stressMockPubSub) PublishChannel(ctx context.Context, event events.ChannelEvent) {}

func BenchmarkChannelMassiveConnect(b *testing.B) {
	testCases := []struct {
		name           string
		goroutineCount int
	}{
		{"1000_users", 1000},
		{"5000_users", 5000},
		{"10000_users", 10000},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()

			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				b.StopTimer()

				// Setup
				broker := messaging.Ensure(nil)
				db, _ := memory.New()
				_, project, _ := db.EnsureDefaultUserAndProject(ctx, "test-user", "test-password")
				manager := channel.NewManager(&stressMockPubSub{}, 60*gotime.Second, 60*gotime.Second, nil, broker, db)

				channelKey := types.ChannelRefKey{
					ProjectID:  project.ID,
					ChannelKey: key.Key("massive-channel"),
				}

				var wg sync.WaitGroup
				var successCount int64
				startSignal := make(chan struct{})

				// Prepare all goroutines
				for g := 0; g < tc.goroutineCount; g++ {
					wg.Add(1)
					go func(gIdx int) {
						defer wg.Done()
						<-startSignal // Wait for signal

						clientID, _ := time.ActorIDFromHex(fmt.Sprintf("%024x", gIdx))
						_, _, err := manager.Attach(ctx, channelKey, clientID)
						if err == nil {
							atomic.AddInt64(&successCount, 1)
						}
					}(g)
				}

				b.StartTimer()
				close(startSignal) // Start all goroutines simultaneously
				wg.Wait()

				assert.Equal(b, int64(tc.goroutineCount), successCount)
			}
		})
	}
}

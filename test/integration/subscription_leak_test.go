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
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/pubsub"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestSubscriptionLeakSelfPrune exercises the dead-stream path that the
// stream handler's defer cannot reach. A subscription is created via
// PubSub.Subscribe and its events channel is never drained, simulating a
// WatchDocument handler whose stream stayed blocked (half-closed TCP,
// hung client). With the failure threshold shortened, the BatchPublisher
// must self-prune and remove the entry from the document's subscription
// map.
//
// The full WatchStream + stuck client flow ends up exercising the same
// pubsub path, but adds HTTP/2 flow-control buffering that lets the
// handler drain events for hundreds of cycles before the buffer fills.
// Bypassing the stream keeps the test deterministic.
func TestSubscriptionLeakSelfPrune(t *testing.T) {
	previous := pubsub.SetDefaultMaxConsecutivePublishFailures(3)
	defer pubsub.SetDefaultMaxConsecutivePublishFailures(previous)

	ctx := context.Background()

	clients := activeClients(t, 1)
	defer deactivateAndCloseClients(t, clients)
	c1 := clients[0]

	docKey := helper.TestKey(t)
	d1 := document.New(docKey)
	assert.NoError(t, c1.Attach(ctx, d1))

	be := defaultServer.Backend()
	project, err := defaultServer.DefaultProject(ctx)
	assert.NoError(t, err)
	docInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, docKey)
	assert.NoError(t, err)
	refKey := types.DocRefKey{ProjectID: project.ID, DocID: docInfo.ID}

	// Subscribe directly so the events channel never drains.
	stuckActor, err := time.ActorIDFromBytes(
		[]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 99},
	)
	assert.NoError(t, err)
	sub, _, err := be.PubSub.Subscribe(ctx, stuckActor, refKey, 0)
	assert.NoError(t, err)
	_ = sub

	assert.True(t, hasSubscriber(be.PubSub.ClientIDs(refKey), stuckActor),
		"stuck actor should be registered before publishes")

	// Drive several DocChanged publishes spread over multiple publish
	// cycles (window = 100ms) so the BatchPublisher accumulates failures
	// against the stuck subscription's full buffer rather than batching
	// them into a single cycle that hits the OnEnqueue dedup cap.
	for i := range 10 {
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k", fmt.Sprintf("v-%d", i))
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		gotime.Sleep(120 * gotime.Millisecond)
	}

	assert.Eventually(t, func() bool {
		return !hasSubscriber(be.PubSub.ClientIDs(refKey), stuckActor)
	}, 5*gotime.Second, 50*gotime.Millisecond,
		"stuck subscription should be reaped after maxFailures publish failures")
}

func hasSubscriber(ids []time.ActorID, target time.ActorID) bool {
	for _, id := range ids {
		if id.Compare(target) == 0 {
			return true
		}
	}
	return false
}

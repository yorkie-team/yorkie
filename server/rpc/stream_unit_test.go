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

package rpc

import (
	"context"
	"errors"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types/events"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/pubsub"
)

// withShortAuthRecheck shortens the stream re-authorization interval for the
// duration of a test and restores it afterwards.
func withShortAuthRecheck(t *testing.T, interval gotime.Duration) {
	t.Helper()

	prev := authRecheckInterval
	authRecheckInterval = interval
	t.Cleanup(func() { authRecheckInterval = prev })
}

// TestStreamEventsRevalidatesAuthorization verifies that an open stream keeps
// re-running its authorization check instead of trusting the single check made
// when it opened. Without this, a token revoked mid-stream keeps delivering
// events until the client disconnects, which no cache setting can shorten.
func TestStreamEventsRevalidatesAuthorization(t *testing.T) {
	withShortAuthRecheck(t, 10*gotime.Millisecond)

	errRevoked := errors.New("revoked")

	t.Run("ends the stream once authorization no longer passes", func(t *testing.T) {
		sub := pubsub.NewDocSubscription(time.InitialActorID)
		defer sub.Close()

		errCh := make(chan error, 1)
		go func() {
			errCh <- streamEvents(
				context.Background(),
				context.Background(),
				sub,
				func(*api.WatchResponse) error { return nil },
				func(events.DocEvent) (*api.WatchResponse, error) { return nil, nil },
				nil,
				func(context.Context) error { return errRevoked },
			)
		}()

		select {
		case err := <-errCh:
			assert.ErrorIs(t, err, errRevoked)
		case <-gotime.After(5 * gotime.Second):
			t.Fatal("streamEvents kept a stream open after authorization was revoked")
		}
	})

	t.Run("keeps the stream open while authorization passes", func(t *testing.T) {
		sub := pubsub.NewDocSubscription(time.InitialActorID)
		defer sub.Close()

		errCh := make(chan error, 1)
		go func() {
			errCh <- streamEvents(
				context.Background(),
				context.Background(),
				sub,
				func(*api.WatchResponse) error { return nil },
				func(events.DocEvent) (*api.WatchResponse, error) { return nil, nil },
				nil,
				func(context.Context) error { return nil },
			)
		}()

		select {
		case err := <-errCh:
			t.Fatalf("streamEvents returned (%v) while authorization still passed", err)
		case <-gotime.After(100 * gotime.Millisecond):
		}
	})
}

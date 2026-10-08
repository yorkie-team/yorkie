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

package client

import (
	"math"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
)

func TestWatchIdleTimeout(t *testing.T) {
	t.Run("no timeout when the server advertises no heartbeat", func(t *testing.T) {
		// A server that sends no heartbeats advertises 0. Timing such a stream
		// out would reconnect on every quiet document.
		assert.Zero(t, watchIdleTimeout(0))
		assert.Zero(t, watchIdleTimeout(-1))
	})

	t.Run("a multiple of the advertised interval", func(t *testing.T) {
		// More than one interval, so a single late heartbeat does not tear
		// down a stream that is merely slow.
		assert.Equal(t, watchIdleTimeoutFactor*20*gotime.Second, watchIdleTimeout(20_000))
		assert.Greater(t, watchIdleTimeout(20_000), 20*gotime.Second)
	})

	t.Run("a huge advertised interval is capped rather than overflowing", func(t *testing.T) {
		// Multiplying an unchecked interval by the factor and by a
		// millisecond overflows int64, and a timer armed with the negative
		// duration that comes out fires at once: the stream would be timed
		// out and reconnected in a loop.
		assert.Equal(t, watchIdleTimeoutMax, watchIdleTimeout(math.MaxInt64))
		assert.Equal(t, watchIdleTimeoutMax, watchIdleTimeout(math.MaxInt64/1_000_000))
		assert.Positive(t, watchIdleTimeout(math.MaxInt64))
	})

	t.Run("an unset initialization reads as no heartbeat", func(t *testing.T) {
		// GetInitialization of a heartbeat or event response is nil, and
		// GetHeartbeatIntervalMs of a nil initialization is 0, so the
		// arithmetic above never sees a bare zero value as an interval.
		var resp *api.WatchResponse
		assert.Zero(t, watchIdleTimeout(resp.GetInitialization().GetHeartbeatIntervalMs()))

		heartbeat := &api.WatchResponse{
			Body: &api.WatchResponse_Heartbeat{Heartbeat: &api.WatchHeartbeat{}},
		}
		assert.Zero(t, watchIdleTimeout(heartbeat.GetInitialization().GetHeartbeatIntervalMs()))
	})
}

func TestHandleWatchHeartbeat(t *testing.T) {
	// A heartbeat must not reach the consumer and must not be reported as an
	// unsupported response type: that error is terminal in the reader loop, so
	// reading it that way would end the pipeline on the first heartbeat.
	d := document.New(key.Key("test-doc"))
	resp, err := handleWatchResponse(&api.WatchResponse{
		Body: &api.WatchResponse_Heartbeat{Heartbeat: &api.WatchHeartbeat{}},
	}, d)
	assert.NoError(t, err)
	assert.Nil(t, resp)
}

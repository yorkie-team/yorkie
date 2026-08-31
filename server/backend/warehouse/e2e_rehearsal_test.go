//go:build starrocksrehearsal

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

package warehouse

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
)

// TestE2EDualReadAgainstStarRocks exercises the real StarRocks read path — dial,
// build, execute, scan — for every metric with SummaryEnabled off vs on, against
// a live StarRocks seeded by scratchpad/rehearsal.sql. It is gated behind the
// `starrocksrehearsal` build tag and a SR_DSN env var, so it never runs in CI
// (StarRocks is not in the harness); run it by hand during a rehearsal:
//
//	SR_DSN='root:@tcp(127.0.0.1:9931)/yorkie' \
//	  go test -tags starrocksrehearsal ./server/backend/warehouse/ -run TestE2E -v
func TestE2EDualReadAgainstStarRocks(t *testing.T) {
	dsn := os.Getenv("SR_DSN")
	if dsn == "" {
		t.Skip("set SR_DSN to run the StarRocks e2e rehearsal")
	}

	off, err := Ensure(&Config{DSN: dsn, SummaryEnabled: false})
	require.NoError(t, err)
	defer func() { _ = off.Close() }()
	on, err := Ensure(&Config{DSN: dsn, SummaryEnabled: true})
	require.NoError(t, err)
	defer func() { _ = on.Close() }()

	ctx := context.Background()
	id := types.ID("p1")
	from := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	counts := []struct {
		name string
		fn   func(Warehouse) (int, error)
		want int
	}{
		{"users", func(w Warehouse) (int, error) { return w.GetActiveUsersCount(ctx, id, from, to) }, 3},
		{"documents", func(w Warehouse) (int, error) { return w.GetActiveDocumentsCount(ctx, id, from, to) }, 3},
		{"channels", func(w Warehouse) (int, error) { return w.GetActiveChannelsCount(ctx, id, from, to) }, 2},
		{"clients", func(w Warehouse) (int, error) { return w.GetActiveClientsCount(ctx, id, from, to) }, 2},
		{"sessions", func(w Warehouse) (int, error) { return w.GetSessionsCount(ctx, id, from, to) }, 5},
		{"peak", func(w Warehouse) (int, error) { return w.GetPeakSessionsPerChannelCount(ctx, id, from, to) }, 2},
	}
	for _, c := range counts {
		t.Run("count_"+c.name, func(t *testing.T) {
			gotOff, err := c.fn(off)
			require.NoError(t, err)
			gotOn, err := c.fn(on)
			require.NoError(t, err)
			assert.Equal(t, c.want, gotOff, "base-only ground truth")
			assert.Equal(t, gotOff, gotOn, "dual-read must equal base-only")
		})
	}

	series := []struct {
		name string
		fn   func(Warehouse) ([]types.MetricPoint, error)
	}{
		{"users", func(w Warehouse) ([]types.MetricPoint, error) { return w.GetActiveUsers(ctx, id, from, to) }},
		{"documents", func(w Warehouse) ([]types.MetricPoint, error) { return w.GetActiveDocuments(ctx, id, from, to) }},
		{"channels", func(w Warehouse) ([]types.MetricPoint, error) { return w.GetActiveChannels(ctx, id, from, to) }},
		{"clients", func(w Warehouse) ([]types.MetricPoint, error) { return w.GetActiveClients(ctx, id, from, to) }},
		{"sessions", func(w Warehouse) ([]types.MetricPoint, error) { return w.GetSessions(ctx, id, from, to) }},
		{"peak", func(w Warehouse) ([]types.MetricPoint, error) { return w.GetPeakSessionsPerChannel(ctx, id, from, to) }},
	}
	for _, s := range series {
		t.Run("series_"+s.name, func(t *testing.T) {
			gotOff, err := s.fn(off)
			require.NoError(t, err)
			gotOn, err := s.fn(on)
			require.NoError(t, err)
			assert.Equal(t, gotOff, gotOn, "dual-read series must equal base-only series")
		})
	}
}

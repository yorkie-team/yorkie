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

package projects

import (
	"context"
	"fmt"
	"math"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// statsRange is a date range the warehouse stats cache precomputes. label keys
// the cached map on the project document; days is the look-back window. The
// dashboard builds the window as [now-days, now+1d] (see converter.FromDateRange),
// so the query span is days+1.
type statsRange struct {
	label string
	days  int
}

// supportedStatsRanges are the ranges the warehouse cache precomputes. These are
// the only ranges the dashboard exposes (7-day default, 4-week secondary); other
// ranges fall back to a live warehouse query.
var supportedStatsRanges = []statsRange{
	{label: "1w", days: 7},
	{label: "4w", days: 28},
}

// statsRangeWindow returns the [from, to] window for a range anchored at now,
// matching converter.FromDateRange so the cached window and the request window
// agree.
func statsRangeWindow(r statsRange, now time.Time) (time.Time, time.Time) {
	return now.AddDate(0, 0, -r.days), now.AddDate(0, 0, 1)
}

// statsRangeLabel classifies a [from, to] window into a cached range label. The
// window endpoints slide with now but the span is range-invariant (days+1), so
// classification is stable regardless of when the request arrives. Returns "" for
// windows that are not cached, signaling the caller to compute them live.
func statsRangeLabel(from, to time.Time) string {
	spanDays := int(math.Round(to.Sub(from).Hours() / 24))
	for _, r := range supportedStatsRanges {
		if spanDays == r.days+1 {
			return r.label
		}
	}
	return ""
}

// warehouseStats bundles the twelve warehouse-backed metrics (six series and six
// range-wide counts) for a single project and window. It is the common shape both
// the live query path and the cache read path resolve to.
type warehouseStats struct {
	activeUsers                 []types.MetricPoint
	activeUsersCount            int
	activeDocuments             []types.MetricPoint
	activeDocumentsCount        int
	activeClients               []types.MetricPoint
	activeClientsCount          int
	activeChannels              []types.MetricPoint
	activeChannelsCount         int
	sessions                    []types.MetricPoint
	sessionsCount               int
	peakSessionsPerChannel      []types.MetricPoint
	peakSessionsPerChannelCount int
}

// warehouseStatsFor resolves the warehouse metrics for the given window. When the
// window is a cached range and the project has a populated cache, it returns the
// cached snapshot without touching the warehouse; otherwise it falls back to a
// live query (cold start, unpopulated cache, or an uncached range).
func warehouseStatsFor(
	ctx context.Context,
	be *backend.Backend,
	id types.ID,
	from, to time.Time,
) (*warehouseStats, error) {
	if label := statsRangeLabel(from, to); label != "" {
		cached, err := be.DB.GetProjectWarehouseStats(ctx, id)
		if err != nil {
			return nil, err
		}
		if r, ok := cached.Ranges[label]; ok && !cached.UpdatedAt.IsZero() {
			return warehouseStatsFromCache(r), nil
		}
	}
	return fetchWarehouseStats(ctx, be, id, from, to)
}

// fetchWarehouseStats queries the twelve warehouse metrics concurrently. errgroup
// cancels the derived context on the first error, so a slow query is not left
// running after the caller has given up.
func fetchWarehouseStats(
	ctx context.Context,
	be *backend.Backend,
	id types.ID,
	from, to time.Time,
) (*warehouseStats, error) {
	var w warehouseStats

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		w.activeUsers, err = be.Warehouse.GetActiveUsers(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		w.activeUsersCount, err = be.Warehouse.GetActiveUsersCount(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		w.activeDocuments, err = be.Warehouse.GetActiveDocuments(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		w.activeDocumentsCount, err = be.Warehouse.GetActiveDocumentsCount(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		w.activeClients, err = be.Warehouse.GetActiveClients(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		w.activeClientsCount, err = be.Warehouse.GetActiveClientsCount(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		w.activeChannels, err = be.Warehouse.GetActiveChannels(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		w.activeChannelsCount, err = be.Warehouse.GetActiveChannelsCount(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		w.sessions, err = be.Warehouse.GetSessions(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		w.sessionsCount, err = be.Warehouse.GetSessionsCount(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		w.peakSessionsPerChannel, err = be.Warehouse.GetPeakSessionsPerChannel(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		w.peakSessionsPerChannelCount, err = be.Warehouse.GetPeakSessionsPerChannelCount(ctx, id, from, to)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("fetch warehouse stats: %w", err)
	}

	return &w, nil
}

// toRange converts the bundle into the cache shape and reports whether it holds
// any data. The refresh task skips writing empty bundles (no warehouse configured,
// or a project with no events), leaving the read path to fall back to a live query.
func (w *warehouseStats) toRange() (database.StatsWarehouseRange, bool) {
	metric := func(series []types.MetricPoint, count int) database.StatsWarehouseMetric {
		return database.StatsWarehouseMetric{Series: metricPointsToCache(series), Count: int64(count)}
	}
	rng := database.StatsWarehouseRange{
		ActiveUsers:            metric(w.activeUsers, w.activeUsersCount),
		ActiveDocuments:        metric(w.activeDocuments, w.activeDocumentsCount),
		ActiveClients:          metric(w.activeClients, w.activeClientsCount),
		ActiveChannels:         metric(w.activeChannels, w.activeChannelsCount),
		Sessions:               metric(w.sessions, w.sessionsCount),
		PeakSessionsPerChannel: metric(w.peakSessionsPerChannel, w.peakSessionsPerChannelCount),
	}
	any := w.activeUsersCount > 0 || w.activeDocumentsCount > 0 || w.activeClientsCount > 0 ||
		w.activeChannelsCount > 0 || w.sessionsCount > 0 || w.peakSessionsPerChannelCount > 0
	return rng, any
}

// refreshProjectWarehouseStats recomputes and caches the warehouse stats for a
// project across all supported ranges. It writes the cache only when some range
// holds data, so projects with no warehouse events keep falling back to a live
// query instead of caching empty snapshots.
func refreshProjectWarehouseStats(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
) error {
	now := time.Now()
	ranges := make(map[string]database.StatsWarehouseRange, len(supportedStatsRanges))
	hasData := false
	for _, sr := range supportedStatsRanges {
		from, to := statsRangeWindow(sr, now)
		ws, err := fetchWarehouseStats(ctx, be, projectID, from, to)
		if err != nil {
			return err
		}
		rng, any := ws.toRange()
		ranges[sr.label] = rng
		if any {
			hasData = true
		}
	}
	if !hasData {
		return nil
	}
	return be.DB.UpdateProjectWarehouseStats(ctx, projectID, ranges, now)
}

// warehouseStatsFromCache reconstructs the bundle from a cached range.
func warehouseStatsFromCache(r database.StatsWarehouseRange) *warehouseStats {
	return &warehouseStats{
		activeUsers:                 cacheToMetricPoints(r.ActiveUsers.Series),
		activeUsersCount:            int(r.ActiveUsers.Count),
		activeDocuments:             cacheToMetricPoints(r.ActiveDocuments.Series),
		activeDocumentsCount:        int(r.ActiveDocuments.Count),
		activeClients:               cacheToMetricPoints(r.ActiveClients.Series),
		activeClientsCount:          int(r.ActiveClients.Count),
		activeChannels:              cacheToMetricPoints(r.ActiveChannels.Series),
		activeChannelsCount:         int(r.ActiveChannels.Count),
		sessions:                    cacheToMetricPoints(r.Sessions.Series),
		sessionsCount:               int(r.Sessions.Count),
		peakSessionsPerChannel:      cacheToMetricPoints(r.PeakSessionsPerChannel.Series),
		peakSessionsPerChannelCount: int(r.PeakSessionsPerChannel.Count),
	}
}

// metricPointsToCache converts API metric points into the cache representation.
func metricPointsToCache(pts []types.MetricPoint) []database.StatsMetricPoint {
	if len(pts) == 0 {
		return nil
	}
	out := make([]database.StatsMetricPoint, len(pts))
	for i, p := range pts {
		out[i] = database.StatsMetricPoint{Time: p.Time, Value: int64(p.Value)}
	}
	return out
}

// cacheToMetricPoints converts cached points back into API metric points.
func cacheToMetricPoints(pts []database.StatsMetricPoint) []types.MetricPoint {
	if len(pts) == 0 {
		return nil
	}
	out := make([]types.MetricPoint, len(pts))
	for i, p := range pts {
		out[i] = types.MetricPoint{Time: p.Time, Value: int(p.Value)}
	}
	return out
}

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
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/warehouse"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/test/helper"
)

const statsTestOwnerID = types.ID("000000000000000000000010")

func setupStatsBackend(t *testing.T) *backend.Backend {
	t.Helper()

	conf := helper.TestConfig()
	conf.Backend.UseDefaultProject = false
	conf.Mongo = &mongo.Config{
		ConnectionTimeout:  "5s",
		ConnectionURI:      "mongodb://localhost:27017",
		YorkieDatabase:     helper.TestDBName() + "-stats",
		PingTimeout:        "5s",
		CacheStatsEnabled:  false,
		CacheStatsInterval: "30s",
		ProjectCacheSize:   helper.MongoProjectCacheSize,
		ProjectCacheTTL:    helper.MongoProjectCacheTTL,
		ClientCacheSize:    helper.MongoClientCacheSize,
		DocCacheSize:       helper.MongoDocCacheSize,
		ChangeCacheSize:    helper.MongoChangeCacheSize,
		VectorCacheSize:    helper.MongoVectorCacheSize,
	}

	metrics, err := prometheus.NewMetrics()
	assert.NoError(t, err)

	be, err := backend.New(
		conf.Backend,
		conf.Mongo,
		conf.Membership,
		conf.Housekeeping,
		metrics,
		nil,
		nil,
	)
	assert.NoError(t, err)

	return be
}

func TestProjectStatsRefresh(t *testing.T) {
	be := setupStatsBackend(t)
	defer func() {
		assert.NoError(t, be.Shutdown())
	}()

	ctx := context.Background()

	// 1. Create a project with a unique name per test run.
	projectName := fmt.Sprintf("%s-%d", t.Name(), gotime.Now().UnixNano())
	project, err := be.DB.CreateProjectInfo(ctx, projectName, statsTestOwnerID)
	assert.NoError(t, err)

	// 4. Before refresh: cached counts should be all zeros and UpdatedAt zero.
	before, err := be.DB.GetProjectStatsCounts(ctx, project.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), before.ClientsCount)
	assert.Equal(t, int64(0), before.DocumentsCount)
	assert.True(t, before.UpdatedAt.IsZero())

	// 2. Activate 3 clients for that project.
	const clientCount = 3
	clientInfos := make([]*database.ClientInfo, 0, clientCount)
	for i := range clientCount {
		clientKey := fmt.Sprintf("%s-client-%d", t.Name(), i)
		ci, err := be.DB.ActivateClient(ctx, project.ID, clientKey, map[string]string{
			"userID": clientKey,
		})
		assert.NoError(t, err)
		clientInfos = append(clientInfos, ci)
	}

	// 3. Create at least one non-removed document for that project.
	docKey := key.Key(fmt.Sprintf("%s-doc", t.Name()))
	_, err = be.DB.FindOrCreateDocInfo(ctx, clientInfos[0].RefKey(), docKey, false)
	assert.NoError(t, err)

	// 5. Run RefreshStats and assert processed >= 1.
	beforeRefresh := gotime.Now()
	_, processed, err := projects.RefreshStats(ctx, be, 10, database.ZeroID)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, processed, 1)

	// 6. After refresh: ClientsCount == 3, DocumentsCount == 1, UpdatedAt recent.
	after, err := be.DB.GetProjectStatsCounts(ctx, project.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(clientCount), after.ClientsCount)
	assert.Equal(t, int64(1), after.DocumentsCount)
	assert.False(t, after.UpdatedAt.IsZero())
	assert.WithinDuration(t, beforeRefresh, after.UpdatedAt, 30*gotime.Second)
}

// peakWarehouse serves a fixed peak-sessions series and falls back to the dummy
// warehouse for every other metric. It substitutes the Warehouse interface at
// the backend seam; no production code is changed. Embedding DummyWarehouse
// keeps it satisfying the interface even when unrelated metrics are added.
type peakWarehouse struct {
	*warehouse.DummyWarehouse

	points []types.MetricPoint
}

// GetPeakSessionsPerChannel returns the fixed series.
func (w *peakWarehouse) GetPeakSessionsPerChannel(
	_ context.Context,
	_ types.ID,
	_ gotime.Time,
	_ gotime.Time,
) ([]types.MetricPoint, error) {
	return w.points, nil
}

// maxValue is the expected value of PeakSessionsPerChannelCount, computed in the
// test rather than borrowed from the production helper.
func maxValue(points []types.MetricPoint) int {
	maxSoFar := 0
	for _, p := range points {
		if p.Value > maxSoFar {
			maxSoFar = p.Value
		}
	}
	return maxSoFar
}

// GetProjectStats fans the reads out with an errgroup and derives the window's
// peak total from the peak series instead of querying it. This pins that
// assembly: the cached counts reach the response, and the peak count is the
// maximum of the very series the response carries.
func TestGetProjectStats(t *testing.T) {
	be := setupStatsBackend(t)
	defer func() {
		assert.NoError(t, be.Shutdown())
	}()

	ctx := context.Background()

	// 1. Create a project with 2 clients and 1 document, then refresh so the
	// cached counts are non-zero and have a refresh time.
	projectName := fmt.Sprintf("%s-%d", t.Name(), gotime.Now().UnixNano())
	project, err := be.DB.CreateProjectInfo(ctx, projectName, statsTestOwnerID)
	assert.NoError(t, err)

	const clientCount = 2
	var firstClient *database.ClientInfo
	for i := range clientCount {
		clientKey := fmt.Sprintf("%s-client-%d", t.Name(), i)
		ci, err := be.DB.ActivateClient(ctx, project.ID, clientKey, map[string]string{
			"userID": clientKey,
		})
		assert.NoError(t, err)
		if i == 0 {
			firstClient = ci
		}
	}
	docKey := key.Key(fmt.Sprintf("%s-doc", t.Name()))
	_, err = be.DB.FindOrCreateDocInfo(ctx, firstClient.RefKey(), docKey, false)
	assert.NoError(t, err)
	_, _, err = projects.RefreshStats(ctx, be, 10, database.ZeroID)
	assert.NoError(t, err)

	to := gotime.Now()
	from := to.AddDate(0, 0, -7)

	// 2. With the warehouse unconfigured, be.Warehouse is the dummy one: every
	// warehouse-backed field comes back empty, so this pins the wiring rather
	// than the numbers. The cached counts and the channel count still come from
	// MongoDB and the cluster, and no node is registered in this test backend,
	// so the channel count is 0.
	stats, err := projects.GetProjectStats(ctx, be, project.ID, from, to)
	assert.NoError(t, err)
	assert.Equal(t, int64(clientCount), stats.ClientsCount)
	assert.Equal(t, int64(1), stats.DocumentsCount)
	assert.False(t, stats.StatsUpdatedAt.IsZero())
	assert.Equal(t, int64(0), stats.ChannelsCount)
	assert.Empty(t, stats.PeakSessionsPerChannel)
	assert.Equal(t, maxValue(stats.PeakSessionsPerChannel), stats.PeakSessionsPerChannelCount)

	// 3. Serve a peak series whose maximum is neither the first nor the last
	// point, so a response that echoed an end of the series, or a separately
	// queried total, would not match.
	points := []types.MetricPoint{
		{Time: from.Unix(), Value: 3},
		{Time: from.AddDate(0, 0, 1).Unix(), Value: 11},
		{Time: from.AddDate(0, 0, 2).Unix(), Value: 4},
	}
	be.Warehouse = &peakWarehouse{DummyWarehouse: &warehouse.DummyWarehouse{}, points: points}

	stats, err = projects.GetProjectStats(ctx, be, project.ID, from, to)
	assert.NoError(t, err)
	assert.Equal(t, points, stats.PeakSessionsPerChannel)
	assert.Equal(t, 11, stats.PeakSessionsPerChannelCount)
	assert.Equal(t, maxValue(stats.PeakSessionsPerChannel), stats.PeakSessionsPerChannelCount)

	// 4. The cached counts are unaffected by the warehouse swap.
	assert.Equal(t, int64(clientCount), stats.ClientsCount)
	assert.Equal(t, int64(1), stats.DocumentsCount)
}

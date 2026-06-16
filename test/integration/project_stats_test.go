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

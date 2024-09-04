//go:build integration && amd64

/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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
	"bytes"
	"context"
	"fmt"
	"log"
	"sort"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"
	monkey "github.com/undefinedlabs/go-mpatch"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/test/helper"
)

const (
	dummyOwnerID              = types.ID("000000000000000000000000")
	otherOwnerID              = types.ID("000000000000000000000001")
	clientDeactivateThreshold = "23h"
)

func setupBackend(t *testing.T) *backend.Backend {
	conf := helper.TestConfig()
	conf.Backend.UseDefaultProject = false
	conf.Mongo = &mongo.Config{
		ConnectionTimeout: "5s",
		ConnectionURI:     "mongodb://localhost:27017",
		YorkieDatabase:    helper.TestDBName() + "-integration",
		PingTimeout:       "5s",
	}

	metrics, err := prometheus.NewMetrics()
	assert.NoError(t, err)

	be, err := backend.New(
		conf.Backend,
		conf.Mongo,
		conf.Housekeeping,
		metrics,
	)
	assert.NoError(t, err)

	return be
}

func TestHousekeeping(t *testing.T) {
	be := setupBackend(t)
	defer func() {
		assert.NoError(t, be.Shutdown())
	}()

	projects := createProjects(t, be.DB)

	t.Run("FindDeactivateCandidates return lastProjectID test", func(t *testing.T) {
		ctx := context.Background()

		fetchSize := 3

		var err error
		lastProjectID := database.DefaultProjectID
		for i := 0; i < len(projects)/fetchSize; i++ {
			lastProjectID, _, err = clients.FindDeactivateCandidates(
				ctx,
				be,
				0,
				fetchSize,
				lastProjectID,
			)
			assert.NoError(t, err)
			assert.Equal(t, projects[((i+1)*fetchSize)-1].ID, lastProjectID)
		}

		lastProjectID, _, err = clients.FindDeactivateCandidates(
			ctx,
			be,
			0,
			fetchSize,
			lastProjectID,
		)
		assert.NoError(t, err)
		assert.Equal(t, projects[fetchSize-(len(projects)%3)-1].ID, lastProjectID)
	})

	t.Run("FindDeactivateCandidates return clients test", func(t *testing.T) {
		ctx := context.Background()

		yesterday := gotime.Now().Add(-24 * gotime.Hour)
		patch, err := monkey.PatchMethod(gotime.Now, func() gotime.Time { return yesterday })
		if err != nil {
			log.Fatal(err)
		}
		clientA, err := be.DB.ActivateClient(ctx, projects[0].ID, fmt.Sprintf("%s-A", t.Name()))
		assert.NoError(t, err)
		clientB, err := be.DB.ActivateClient(ctx, projects[0].ID, fmt.Sprintf("%s-B", t.Name()))
		assert.NoError(t, err)
		if err = patch.Unpatch(); err != nil {
			log.Fatal(err)
		}

		clientC, err := be.DB.ActivateClient(ctx, projects[0].ID, fmt.Sprintf("%s-C", t.Name()))
		assert.NoError(t, err)

		_, candidates, err := clients.FindDeactivateCandidates(
			ctx,
			be,
			10,
			10,
			database.DefaultProjectID,
		)

		assert.NoError(t, err)
		assert.Len(t, candidates, 2)
		assert.Equal(t, candidates[0].ID, clientA.ID)
		assert.Equal(t, candidates[1].ID, clientB.ID)
		assert.NotContains(t, candidates, clientC)
	})
}

func createProjects(t *testing.T, db database.Database) []*database.ProjectInfo {
	t.Helper()

	ctx := context.Background()

	projects := make([]*database.ProjectInfo, 0)
	for i := 0; i < 10; i++ {
		p, err := db.CreateProjectInfo(ctx, fmt.Sprintf("%d project", i), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)
		projects = append(projects, p)
		p, err = db.CreateProjectInfo(ctx, fmt.Sprintf("%d project", i), otherOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)
		projects = append(projects, p)
	}

	sort.Slice(projects, func(i, j int) bool {
		iBytes, err := projects[i].ID.Bytes()
		assert.NoError(t, err)
		jBytes, err := projects[j].ID.Bytes()
		assert.NoError(t, err)
		return bytes.Compare(iBytes, jBytes) < 0
	})

	return projects
}

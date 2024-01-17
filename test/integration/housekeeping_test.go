//go:build integration && amd64

/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

	monkey "github.com/undefinedlabs/go-mpatch"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/backend/sync/memory"
	"github.com/yorkie-team/yorkie/test/helper"
)

const (
	dummyOwnerID              = types.ID("000000000000000000000003")
	otherOwnerID              = types.ID("000000000000000000000004")
	clientDeactivateThreshold = "23h"
)

func setupTest(t *testing.T) *mongo.Client {
	config := &mongo.Config{
		ConnectionTimeout: "5s",
		ConnectionURI:     "mongodb://localhost:27017",
		YorkieDatabase:    helper.TestDBName() + "-integration",
		PingTimeout:       "5s",
	}
	assert.NoError(t, config.Validate())

	cli, err := mongo.Dial(config)
	assert.NoError(t, err)

	return cli
}

func TestHousekeeping(t *testing.T) {
	config := helper.TestConfig()
	db := setupTest(t)

	projects := createProjects(t, db)

	coordinator := memory.NewCoordinator(nil)

	h, err := housekeeping.New(config.Housekeeping, db, coordinator)
	assert.NoError(t, err)

	t.Run("FindDeactivateCandidates return lastProjectID test", func(t *testing.T) {
		ctx := context.Background()

		fetchSize := 3
		lastProjectID := database.DefaultProjectID

		for i := 0; i < len(projects)/fetchSize; i++ {
			lastProjectID, _, err = h.FindDeactivateCandidates(
				ctx,
				0,
				fetchSize,
				lastProjectID,
			)
			assert.NoError(t, err)
			assert.Equal(t, projects[((i+1)*fetchSize)-1].ID, lastProjectID)
		}

		lastProjectID, _, err = h.FindDeactivateCandidates(
			ctx,
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
		clientA, err := db.ActivateClient(ctx, projects[0].ID, fmt.Sprintf("%s-A", t.Name()))
		assert.NoError(t, err)
		clientB, err := db.ActivateClient(ctx, projects[0].ID, fmt.Sprintf("%s-B", t.Name()))
		assert.NoError(t, err)
		err = patch.Unpatch()
		if err != nil {
			log.Fatal(err)
		}

		clientC, err := db.ActivateClient(ctx, projects[0].ID, fmt.Sprintf("%s-C", t.Name()))
		assert.NoError(t, err)

		_, candidates, err := h.FindDeactivateCandidates(
			ctx,
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

func createProjects(t *testing.T, db *mongo.Client) []*database.ProjectInfo {
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

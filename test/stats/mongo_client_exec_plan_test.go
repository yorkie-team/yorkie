//go:build stats

/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

package stats

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	gomongo "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/test/helper"
)

const (
	execDBName                = "test-yorkie-meta-server"
	dummyProjectID            = types.ID("000000000000000000000000")
	projectOneID              = types.ID("000000000000000000000001")
	projectTwoID              = types.ID("000000000000000000000002")
	dummyOwnerID              = types.ID("000000000000000000000000")
	dummyClientID             = types.ID("000000000000000000000000")
	clientDeactivateThreshold = "1h"
	pageSize                  = 20
)

type CountPerID struct {
	ID    primitive.ObjectID
	Count int64
}

func getQueryExecPlan(ctx context.Context, db *gomongo.Database, command bson.D) (string, error) {
	var result bson.M
	err := db.RunCommand(
		ctx,
		bson.D{{Key: "explain", Value: command}},
		options.RunCmd().SetReadPreference(readpref.Primary()),
	).Decode(&result)
	if err != nil {
		return "", err
	}
	prettyJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", nil
	}
	return string(prettyJSON), nil
}

func TestClientExecPlan(t *testing.T) {
	cli, err := helper.SetupRawMongoClient(execDBName)
	db := cli.Database(execDBName)
	assert.NoError(t, err)

	t.Run("Find Documents", func(t *testing.T) {
		ctx := context.Background()
		filter := bson.M{
			"project_id": bson.M{
				"$eq": dummyProjectID,
			},
			"removed_at": bson.M{
				"$exists": false,
			},
		}
		command := bson.D{
			{Key: "find", Value: "documents"},
			{Key: "filter", Value: filter},
			{Key: "limit", Value: int64(pageSize)},
		}
		plan, err := getQueryExecPlan(ctx, db, command)
		assert.NoError(t, err)
		t.Log(plan)
	})

	t.Run("Find Clients", func(t *testing.T) {
		ctx := context.Background()
		cursor, err := db.Collection(mongo.ColClients).Aggregate(ctx, gomongo.Pipeline{
			{{Key: "$sortByCount", Value: "$project_id"}},
			{{Key: "$limit", Value: 1}},
		})
		assert.NoError(t, err)

		var infos []*CountPerID
		assert.NoError(t, cursor.All(ctx, &infos))
		assert.Equal(t, 1, len(infos))

		clientDeactivateThreshold, err := time.ParseDuration(clientDeactivateThreshold)
		assert.NoError(t, err)
		filter := bson.M{
			"project_id": infos[0].ID,
			"status":     database.ClientActivated,
			"updated_at": bson.M{
				"$lte": time.Now().Add(-clientDeactivateThreshold),
			},
		}
		command := bson.D{
			{Key: "find", Value: "clients"},
			{Key: "filter", Value: filter},
		}
		plan, err := getQueryExecPlan(ctx, db, command)
		assert.NoError(t, err)
		t.Log(plan)
	})

}

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

package v056

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/yorkie-team/yorkie/server/backend/database"
)

const (
	// StatusKey is the key of the status field.
	StatusKey = "status"
)

func validateDetach(ctx context.Context, collection *mongo.Collection, filter bson.M) ([]string, error) {
	var failedClients []string
	totalCount, err := collection.CountDocuments(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	fmt.Printf("Validation check for %d clients\n", totalCount)

	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return nil, err
	}

	for cursor.Next(ctx) {
		var info database.ClientInfo
		if err := cursor.Decode(&info); err != nil {
			return nil, fmt.Errorf("decode client info: %w", err)
		}

		// 01. ensure deactivated
		if info.Status != database.ClientDeactivated {
			continue
		}

		// 02. ensure detached
		hasAttachedDocs := false
		for _, clientDocInfo := range info.Documents {
			if clientDocInfo.Status == database.DocumentAttached {
				hasAttachedDocs = true
				break
			}
		}
		if hasAttachedDocs {
			failedClients = append(failedClients, info.ID.String())
		}
	}

	return failedClients, nil
}

func progressMigrationBatch(
	ctx context.Context,
	collection *mongo.Collection,
	infos []*database.ClientInfo,
) error {
	for _, info := range infos {
		result := collection.FindOneAndUpdate(ctx, bson.M{
			"project_id": info.ProjectID,
			"_id":        info.ID,
		}, bson.M{
			"$set": bson.M{
				StatusKey:    database.ClientActivated,
				"updated_at": time.Now().AddDate(-1, 0, 0),
			},
		})
		if result.Err() != nil {
			if result.Err() == mongo.ErrNoDocuments {
				return fmt.Errorf("%s: %w", info.Key, database.ErrClientNotFound)
			}
			return fmt.Errorf("update client info: %w", result.Err())
		}
	}
	return nil
}

// ReactivateClients migrates the client collection to activate the clients
// that are in a deactivated but have attached documents.
func ReactivateClients(
	ctx context.Context,
	db *mongo.Client,
	databaseName string,
	batchSize int,
) error {
	collection := db.Database(databaseName).Collection("clients")

	filter := bson.M{
		"status": "deactivated",
		"documents": bson.M{
			"$ne":     nil,
			"$exists": true,
		},
		"$expr": bson.M{
			"$gt": bson.A{
				bson.M{
					"$size": bson.M{
						"$filter": bson.M{
							"input": bson.M{
								"$objectToArray": "$documents",
							},
							"as": "doc",
							"cond": bson.M{
								"$eq": bson.A{"$$doc.v.status", "attached"},
							},
						},
					},
				},
				0,
			},
		},
	}

	// 01. Count the number of target clients
	totalCount, err := collection.CountDocuments(ctx, filter)
	if err != nil {
		return err
	}
	fmt.Printf("total clients: %d\n", totalCount)

	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return err
	}

	var infos []*database.ClientInfo

	batchCount := 1
	for cursor.Next(ctx) {
		var clientInfo database.ClientInfo
		if err := cursor.Decode(&clientInfo); err != nil {
			return fmt.Errorf("decode client info: %w", err)
		}

		infos = append(infos, &clientInfo)

		if len(infos) >= batchSize {
			if err := progressMigrationBatch(ctx, collection, infos); err != nil {
				return err
			}

			// TODO(raararaara): print progress
			infos = infos[:0]
			batchCount++
		}
	}
	if len(infos) > 0 {
		if err := progressMigrationBatch(ctx, collection, infos); err != nil {
			return err
		}
	}

	res, err := validateDetach(ctx, collection, filter)
	if err != nil {
		return err
	}
	fmt.Printf("Number of failed clients: %d\n", len(res))
	if 0 < len(res) && len(res) < 100 {
		fmt.Print(res)
	}

	return nil
}

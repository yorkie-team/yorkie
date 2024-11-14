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
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/yorkie-team/yorkie/server/backend/database"
)

const (
	// StatusKey is the key of the status field.
	StatusKey = "status"
)

// validateDetach checks whether there are deactivated clients with attached documents.
func validateDetach(ctx context.Context, collection *mongo.Collection, filter bson.M) int {
	var failCount int
	totalCount, err := collection.CountDocuments(ctx, bson.M{})
	if err != nil {
		fmt.Printf("[Validation] Count total document failed\n")
		return 0
	}
	fmt.Printf("[Validation] Validation check for %d clients\n", totalCount)

	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		fmt.Printf("[Validation] Find document failed\n")
		return 0
	}

	for cursor.Next(ctx) {
		var info database.ClientInfo
		if err := cursor.Decode(&info); err != nil {
			fmt.Printf("[Validation] decode client info failed\n")
			failCount++
			continue
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
			fmt.Printf("[Validation] Client %s has attached documents\n", info.Key)
			failCount++
		}
	}

	return failCount
}

// processMigrationBatch processes the migration batch.
func processMigrationBatch(
	ctx context.Context,
	collection *mongo.Collection,
	infos []*database.ClientInfo,
) {
	for _, info := range infos {
		result := collection.FindOneAndUpdate(ctx, bson.M{
			"project_id": info.ProjectID,
			"_id":        info.ID,
		}, bson.M{
			"$set": bson.M{
				StatusKey: database.ClientActivated,
			},
		})
		if result.Err() != nil {
			if errors.Is(result.Err(), mongo.ErrNoDocuments) {
				fmt.Printf("[Migration Batch] Client not found: %s\n", info.Key)
			} else {
				fmt.Printf("[Migration Batch] Failed to update client info: %v\n", result.Err())
			}
		}
	}
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
	done := 0
	for cursor.Next(ctx) {
		var clientInfo database.ClientInfo
		if err := cursor.Decode(&clientInfo); err != nil {
			return fmt.Errorf("decode client info: %w", err)
		}

		infos = append(infos, &clientInfo)

		if len(infos) >= batchSize {
			processMigrationBatch(ctx, collection, infos)
			done += len(infos)

			percentage := int(float64(batchSize*batchCount) / float64(totalCount) * 100)
			fmt.Printf("%s.clients migration progress: %d%%(%d/%d)\n", databaseName, percentage, done, totalCount)
			infos = infos[:0]
			batchCount++
		}
	}
	if len(infos) > 0 {
		processMigrationBatch(ctx, collection, infos)
		done += len(infos)
	}
	fmt.Printf("%s.clients migration progress: %d%%(%d/%d)\n", databaseName, 100, done, totalCount)

	fmt.Printf("Number of failed clients: %d\n", validateDetach(ctx, collection, filter))

	return nil
}

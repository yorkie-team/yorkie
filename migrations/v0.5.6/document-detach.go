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
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/yorkie-team/yorkie/server/backend/database"
)

const (
	// StatusKey is the key of the status field.
	StatusKey = "status"
)

// validateDetach checks whether there are deactivated clients with attached documents.
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
				StatusKey:    database.ClientActivated,
				"updated_at": time.Now().AddDate(-1, 0, 0),
			},
		})
		if result.Err() != nil {
			if errors.Is(result.Err(), mongo.ErrNoDocuments) {
				_ = fmt.Errorf("%s: %w", info.Key, database.ErrClientNotFound)
			} else {
				_ = fmt.Errorf("update client info: %w", result.Err())
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

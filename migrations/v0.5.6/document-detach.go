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
	"github.com/yorkie-team/yorkie/server/backend/database"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func progressMigrationBach(
	_ context.Context,
	_ *mongo.Collection,
	_ []database.ClientInfo,
) error {
	return nil
}

// DetachDocumentsFromDeactivatedClients migrates the client collection
// to detach documents attached to a deactivated client.
func DetachDocumentsFromDeactivatedClients(
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

	//res := collection.FindOne(ctx, filter)
	//if res.Err() == mongo.ErrNoDocuments {
	//	return fmt.Errorf("no doc")
	//}
	//if res.Err() != nil {
	//	return fmt.Errorf("error occurs")
	//}
	//
	//clientInfo := database.ClientInfo{}
	//if err := res.Decode(&clientInfo); err != nil {
	//	return fmt.Errorf("decode error")
	//}
	//
	//fmt.Printf("%d %s\n", batchSize, clientInfo.ID.String())

	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return err
	}

	var infos []database.ClientInfo

	batchCount := 1
	for cursor.Next(ctx) {
		var clientInfo database.ClientInfo
		if err := cursor.Decode(&clientInfo); err != nil {
			return fmt.Errorf("decode client info: %w", err)
		}

		infos = append(infos, clientInfo)

		if len(infos) >= batchSize {
			if err := progressMigrationBach(ctx, collection, infos); err != nil {
				return err
			}

			// TODO(raararaara): print progress
			infos = infos[:0]
			batchCount++
		}
	}
	if len(infos) > 0 {
		if err := progressMigrationBach(ctx, collection, infos); err != nil {
			return err
		}
	}

	// TODO(raararaara): validation check

	return nil
}

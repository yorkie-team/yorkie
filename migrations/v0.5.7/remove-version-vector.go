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

package v057

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func processDeletionBatch(
	ctx context.Context,
	collection *mongo.Collection,
	documents []bson.M,
) error {
	var writeModels []mongo.WriteModel

	for _, doc := range documents {
		writeModel := mongo.NewUpdateOneModel().SetFilter(bson.M{
			"project_id": doc["project_id"],
			"doc_id":     doc["doc_id"],
			"server_seq": doc["server_seq"],
		}).SetUpdate(bson.M{
			"$unset": bson.M{
				"version_vector": "",
			},
		})
		writeModels = append(writeModels, writeModel)
	}

	if len(writeModels) > 0 {
		_, err := collection.BulkWrite(ctx, writeModels)
		if err != nil {
			return fmt.Errorf("execute bulk write: %w", err)
		}
	}

	return nil
}

// RemoveVersionVector migrates the changes collection to remove version vector field.
func RemoveVersionVector(ctx context.Context, db *mongo.Client, databaseName string, batchSize int) error {
	collection := db.Database(databaseName).Collection("changes")
	totalCount, err := collection.CountDocuments(ctx, bson.M{})
	if err != nil {
		return err
	}
	batchCount := 1
	prevPercentage := 0

	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		return err
	}

	var documents []bson.M

	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return fmt.Errorf("decode document: %w", err)
		}

		documents = append(documents, doc)

		if len(documents) >= batchSize {
			if err := processDeletionBatch(ctx, collection, documents); err != nil {
				return err
			}

			percentage := int(float64(batchSize*batchCount) / float64(totalCount) * 100)
			if percentage != prevPercentage {
				fmt.Printf("%s.changes version vector removal %d%% completed \n", databaseName, percentage)
				prevPercentage = percentage
			}

			documents = documents[:0]
			batchCount++
		}
	}

	if len(documents) > 0 {
		if err := processDeletionBatch(ctx, collection, documents); err != nil {
			return fmt.Errorf("process final batch: %w", err)
		}
	}

	fmt.Println("remove version vector completed")

	return nil
}

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

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// validatePresenceChangeMigration validates if all string presence changes are properly migrated
func validatePresenceChangeMigration(ctx context.Context, db *mongo.Client, databaseName string) error {
	collection := db.Database(databaseName).Collection("changes")

	cursor, err := collection.Find(ctx, bson.M{
		"presence_change": bson.M{
			"$type": "string",
		},
	})
	if err != nil {
		return err
	}

	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return fmt.Errorf("decode document: %w", err)
		}

		if presenceChange, ok := doc["presence_change"]; ok {
			if _, isString := presenceChange.(string); isString {
				return fmt.Errorf("found presence change still stored as string")
			}
		}
	}

	return nil
}

// processMigrationBatchPresence processes a batch of presence change migrations
func processMigrationBatchPresence(
	ctx context.Context,
	collection *mongo.Collection,
	docs []bson.M,
) error {
	var operations []mongo.WriteModel

	for _, doc := range docs {
		if presenceChange, ok := doc["presence_change"]; ok {
			if presenceChangeStr, isString := presenceChange.(string); isString {
				var operation *mongo.UpdateOneModel

				if presenceChangeStr == "" {
					operation = mongo.NewUpdateOneModel().SetFilter(bson.M{
						"_id": doc["_id"],
					}).SetUpdate(bson.M{
						"$set": bson.M{
							"presence_change": nil,
						},
					})
				} else {
					operation = mongo.NewUpdateOneModel().SetFilter(bson.M{
						"_id": doc["_id"],
					}).SetUpdate(bson.M{
						"$set": bson.M{
							"presence_change": []byte(presenceChangeStr),
						},
					})
				}

				operations = append(operations, operation)
			}
		}
	}

	if len(operations) > 0 {
		_, err := collection.BulkWrite(ctx, operations)
		if err != nil {
			return fmt.Errorf("execute bulk write: %w", err)
		}
	}

	return nil
}

// MigratePresenceChange migrates presence changes from string to byte array format
func MigratePresenceChange(ctx context.Context, db *mongo.Client, databaseName string, batchSize int) error {
	collection := db.Database(databaseName).Collection("changes")
	filter := bson.M{
		"presence_change": bson.M{
			"$type": "string",
		},
	}

	totalCount, err := collection.CountDocuments(ctx, filter)
	if err != nil {
		return err
	}
	if totalCount == 0 {
		fmt.Println("No data found to migrate")
		return nil
	}

	batchCount := 1
	prevPercentage := 0
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return err
	}

	var docs []bson.M

	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return fmt.Errorf("decode document: %w", err)
		}

		docs = append(docs, doc)

		if len(docs) >= batchSize {
			if err := processMigrationBatchPresence(ctx, collection, docs); err != nil {
				return err
			}

			percentage := int(float64(batchSize*batchCount) / float64(totalCount) * 100)

			if percentage != prevPercentage {
				fmt.Printf("%s.changes presence change migration %d%% completed \n", databaseName, percentage)
				prevPercentage = percentage
			}

			docs = docs[:0]
			batchCount++
		}
	}

	if len(docs) > 0 {
		if err := processMigrationBatchPresence(ctx, collection, docs); err != nil {
			return fmt.Errorf("process final batch: %w", err)
		}
	}

	if err := validatePresenceChangeMigration(ctx, db, databaseName); err != nil {
		return err
	}

	fmt.Printf("%s.changes presence change migration completed: %d converted \n", databaseName, totalCount)
	return nil
}

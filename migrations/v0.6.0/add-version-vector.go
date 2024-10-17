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

package v060

import (
	"context"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

func validateAddVersionVector(ctx context.Context, db *mongo.Client, databaseName string) error {
	collection := db.Database(databaseName).Collection("changes")

	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		return err
	}

	for cursor.Next(ctx) {
		var info database.ChangeInfo
		if err := cursor.Decode(&info); err != nil {
			return fmt.Errorf("decode change info: %w", err)
		}

		versionVector := info.VersionVector
		actors, err := versionVector.Keys()
		if err != nil {
			return err
		}
		lamport := info.Lamport

		if len(actors) > 1 {
			return fmt.Errorf("found %d actor in version vector", len(actors))
		}

		if versionVector.VersionOf(actors[0]) != lamport {
			return fmt.Errorf("wrong lamport in version vector")
		}
	}

	return nil
}

func processMigrationBatch(
	ctx context.Context,
	collection *mongo.Collection,
	infos []database.ChangeInfo) error {
	var operations []mongo.WriteModel

	for _, info := range infos {
		versionVector := time.NewVersionVector()
		actorID, err := info.ActorID.ToActorID()
		if err != nil {
			return err
		}

		versionVector.Set(actorID, info.Lamport)
		operation := mongo.NewUpdateOneModel().SetFilter(bson.M{
			"project_id": info.ProjectID,
			"doc_id":     info.DocID,
			"server_seq": info.ServerSeq,
		}).SetUpdate(bson.M{"$set": bson.M{
			"version_vector": versionVector,
		}})
		operations = append(operations, operation)
	}

	if len(operations) > 0 {
		_, err := collection.BulkWrite(ctx, operations)
		if err != nil {
			return fmt.Errorf("execute bulk write: %w", err)
		}
	}

	return nil
}

// AddVersionVector runs migrations for add version vector
func AddVersionVector(ctx context.Context, db *mongo.Client, databaseName string, batchSize int) error {
	collection := db.Database(databaseName).Collection("changes")
	totalCount, err := collection.CountDocuments(ctx, bson.M{})
	if err != nil {
		return err
	}
	batchCount := 1

	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		return err
	}

	var infos []database.ChangeInfo

	for cursor.Next(ctx) {
		var info database.ChangeInfo
		if err := cursor.Decode(&info); err != nil {
			return fmt.Errorf("decode change info: %w", err)
		}

		infos = append(infos, info)

		if len(infos) >= batchSize {
			if err := processMigrationBatch(ctx, collection, infos); err != nil {
				return err
			}

			percentage := (float64(batchSize*batchCount) / float64(totalCount)) * 100
			rounded := math.Round(percentage)
			fmt.Printf("%s.changes migration %0.f%% completed \n", databaseName, rounded)

			infos = infos[:0]
			batchCount++
		}
	}

	if len(infos) > 0 {
		if err := processMigrationBatch(ctx, collection, infos); err != nil {
			return fmt.Errorf("process final batch: %w", err)
		}
	}

	err = validateAddVersionVector(ctx, db, databaseName)
	if err != nil {
		return err
	}

	fmt.Println("add version vector completed")

	return nil
}

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

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

type changeInfoWithoutVersionVector struct {
	ID             types.ID `bson:"_id"`
	ProjectID      types.ID `bson:"project_id"`
	DocID          types.ID `bson:"doc_id"`
	ServerSeq      int64    `bson:"server_seq"`
	ClientSeq      uint32   `bson:"client_seq"`
	Lamport        int64    `bson:"lamport"`
	ActorID        types.ID `bson:"actor_id"`
	Message        string   `bson:"message"`
	Operations     [][]byte `bson:"operations"`
	PresenceChange string   `bson:"presence_change"`
}

func processMigrationBatch(
	ctx context.Context,
	collection *mongo.Collection,
	infos []changeInfoWithoutVersionVector) error {
	var operations []mongo.WriteModel

	for _, info := range infos {
		versionVector := time.NewVersionVector()
		actorID, err := info.ActorID.ToActorID()
		if err != nil {
			return err
		}
		versionVector.Set(actorID, info.Lamport)

		filter := bson.M{
			"doc_id":     info.DocID,
			"project_id": info.ProjectID,
			"server_seq": info.ServerSeq,
		}
		update := bson.M{
			"$set": bson.M{
				"version_vector": versionVector,
			},
		}

		operation := mongo.NewUpdateOneModel().SetFilter(filter).SetUpdate(update)
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
func AddVersionVector(ctx context.Context, db *mongo.Client, batchSize int) error {
	collection := db.Database("yorkie-meta").Collection("changes")

	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		return err
	}

	var infos []changeInfoWithoutVersionVector

	for cursor.Next(ctx) {
		var info changeInfoWithoutVersionVector
		if err := cursor.Decode(&info); err != nil {
			return fmt.Errorf("failed to decode document: %w", err)
		}

		infos = append(infos, info)

		if len(infos) >= batchSize {
			if err := processMigrationBatch(ctx, collection, infos); err != nil {
				return fmt.Errorf("failed to process batch: %w", err)
			}

			infos = infos[:0]
		}
	}

	if len(infos) > 0 {
		if err := processMigrationBatch(ctx, collection, infos); err != nil {
			return fmt.Errorf("failed to process final batch: %w", err)
		}
	}

	return nil
}

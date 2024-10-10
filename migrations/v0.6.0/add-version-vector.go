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

// Package v060 provides migration for v0.6.0
package v060

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/yorkie-team/yorkie/api/types"
)

type changeInfo struct {
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

// RunMigration runs migrations for package version
func RunMigration(ctx context.Context, db *mongo.Client) error {
	collection := db.Database("yorkie-meta").Collection("changes")

	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		return err
	}

	var infos []*changeInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return fmt.Errorf("fetch project infos: %w", err)
	}

	for _, info := range infos {
		fmt.Println(info.ActorID.String(), info.Lamport)
	}

	return nil
}

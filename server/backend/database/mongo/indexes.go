/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

const (
	// ColProjects represents the projects collection in the database.
	ColProjects = "projects"
	// ColUsers represents the users collection in the database.
	ColUsers = "users"
	// ColClients represents the clients collection in the database.
	ColClients = "clients"
	// ColDocuments represents the documents collection in the database.
	ColDocuments = "documents"
	// ColChanges represents the changes collection in the database.
	ColChanges = "changes"
	// ColSnapshots represents the snapshots collection in the database.
	ColSnapshots = "snapshots"
	// ColSyncedSeqs represents the syncedseqs collection in the database.
	ColSyncedSeqs = "syncedseqs"
)

// Collections represents the list of all collections in the database.
var Collections = []string{
	ColProjects,
	ColUsers,
	ColClients,
	ColDocuments,
	ColChanges,
	ColSnapshots,
	ColSyncedSeqs,
}

type collectionInfo struct {
	name    string
	indexes []mongo.IndexModel
}

// Below are names and indexes information of Collections that stores Yorkie data.
var collectionInfos = []collectionInfo{
	{
		name: ColProjects,
		indexes: []mongo.IndexModel{{
			Keys: bsonx.Doc{
				{Key: "owner", Value: bsonx.Int32(1)},
				{Key: "name", Value: bsonx.Int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}, {
			Keys:    bsonx.Doc{{Key: "public_key", Value: bsonx.Int32(1)}},
			Options: options.Index().SetUnique(true),
		}, {
			Keys:    bsonx.Doc{{Key: "secret_key", Value: bsonx.Int32(1)}},
			Options: options.Index().SetUnique(true),
		}},
	},
	{
		name: ColUsers,
		indexes: []mongo.IndexModel{{
			Keys:    bsonx.Doc{{Key: "username", Value: bsonx.Int32(1)}},
			Options: options.Index().SetUnique(true),
		}},
	},
	{
		name: ColClients,
		indexes: []mongo.IndexModel{{
			Keys: bsonx.Doc{
				{Key: "project_id", Value: bsonx.Int32(1)}, // shard key
				{Key: "key", Value: bsonx.Int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}, {
			Keys: bsonx.Doc{
				{Key: "project_id", Value: bsonx.Int32(1)},
				{Key: "status", Value: bsonx.Int32(1)},
				{Key: "updated_at", Value: bsonx.Int32(1)},
			},
		}, {
			Keys: bsonx.Doc{
				{Key: "documents.$**", Value: bsonx.Int32(1)},
			},
		}},
	},
	{
		name: ColDocuments,
		indexes: []mongo.IndexModel{{
			Keys: bsonx.Doc{
				{Key: "project_id", Value: bsonx.Int32(1)}, // shard key
				{Key: "key", Value: bsonx.Int32(1)},
			},
			Options: options.Index().SetPartialFilterExpression(
				bsonx.Doc{
					{Key: "removed_at", Value: bsonx.Null()},
				},
			).SetUnique(true),
		}},
	}, {
		name: ColChanges,
		indexes: []mongo.IndexModel{{
			Keys: bsonx.Doc{
				{Key: "doc_id", Value: bsonx.Int32(1)}, // shard key
				{Key: "project_id", Value: bsonx.Int32(1)},
				{Key: "server_seq", Value: bsonx.Int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}},
	}, {
		name: ColSnapshots,
		indexes: []mongo.IndexModel{{
			Keys: bsonx.Doc{
				{Key: "doc_id", Value: bsonx.Int32(1)}, // shard key
				{Key: "project_id", Value: bsonx.Int32(1)},
				{Key: "server_seq", Value: bsonx.Int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}},
	}, {
		name: ColSyncedSeqs,
		indexes: []mongo.IndexModel{{
			Keys: bsonx.Doc{
				{Key: "doc_id", Value: bsonx.Int32(1)}, // shard key
				{Key: "project_id", Value: bsonx.Int32(1)},
				{Key: "client_id", Value: bsonx.Int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}, {
			Keys: bsonx.Doc{
				{Key: "doc_id", Value: bsonx.Int32(1)},
				{Key: "project_id", Value: bsonx.Int32(1)},
				{Key: "lamport", Value: bsonx.Int32(1)},
				{Key: "actor_id", Value: bsonx.Int32(1)},
			},
		}},
	},
}

func ensureIndexes(ctx context.Context, db *mongo.Database) error {
	for _, info := range collectionInfos {
		_, err := db.Collection(info.name).Indexes().CreateMany(ctx, info.indexes)
		if err != nil {
			return fmt.Errorf("create indexes: %w", err)
		}
	}
	return nil
}

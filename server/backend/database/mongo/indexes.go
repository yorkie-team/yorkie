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

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

const (
	colProjects   = "projects"
	colUsers      = "users"
	colClients    = "clients"
	colDocuments  = "documents"
	colChanges    = "changes"
	colSnapshots  = "snapshots"
	colSyncedSeqs = "syncedseqs"
)

type collectionInfo struct {
	name    string
	indexes []mongo.IndexModel
}

// Below are names and indexes information of collections that stores Yorkie data.
var collectionInfos = []collectionInfo{
	{
		name: colProjects,
		indexes: []mongo.IndexModel{{
			Keys:    bsonx.Doc{{Key: "name", Value: bsonx.Int32(1)}},
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
		name: colUsers,
		indexes: []mongo.IndexModel{{
			Keys:    bsonx.Doc{{Key: "email", Value: bsonx.Int32(1)}},
			Options: options.Index().SetUnique(true),
		}},
	},
	{
		name: colClients,
		indexes: []mongo.IndexModel{{
			Keys: bsonx.Doc{
				{Key: "project_id", Value: bsonx.Int32(1)},
				{Key: "key", Value: bsonx.Int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}, {
			Keys: bsonx.Doc{
				{Key: "status", Value: bsonx.Int32(1)},
				{Key: "updated_at", Value: bsonx.Int32(1)},
			},
		}},
	}, {
		name: colDocuments,
		indexes: []mongo.IndexModel{{
			Keys: bsonx.Doc{
				{Key: "project_id", Value: bsonx.Int32(1)},
				{Key: "key", Value: bsonx.Int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}},
	}, {
		name: colChanges,
		indexes: []mongo.IndexModel{{
			Keys: bsonx.Doc{
				{Key: "doc_id", Value: bsonx.Int32(1)},
				{Key: "server_seq", Value: bsonx.Int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}},
	}, {
		name: colSnapshots,
		indexes: []mongo.IndexModel{{
			Keys: bsonx.Doc{
				{Key: "doc_id", Value: bsonx.Int32(1)},
				{Key: "server_seq", Value: bsonx.Int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}},
	}, {
		name: colSyncedSeqs,
		indexes: []mongo.IndexModel{{
			Keys: bsonx.Doc{
				{Key: "doc_id", Value: bsonx.Int32(1)},
				{Key: "client_id", Value: bsonx.Int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}, {
			Keys: bsonx.Doc{
				{Key: "doc_id", Value: bsonx.Int32(1)},
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
			return err
		}
	}
	return nil
}

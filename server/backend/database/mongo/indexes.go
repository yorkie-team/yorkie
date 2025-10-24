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

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	// ColClusterNodes represents the cluster nodes collection in the database.
	ColClusterNodes = "clusternodes"
	// ColProjects represents the projects collection in the database.
	ColProjects = "projects"
	// ColUsers represents the users collection in the database.
	ColUsers = "users"
	// ColClients represents the clients collection in the database.
	ColClients = "clients"
	// ColDocuments represents the documents collection in the database.
	ColDocuments = "documents"
	// ColSchemas represents the schemas collection in the database.
	ColSchemas = "schemas"
	// ColChanges represents the changes collection in the database.
	ColChanges = "changes"
	// ColSnapshots represents the snapshots collection in the database.
	ColSnapshots = "snapshots"
	// ColVersionVectors represents the versionvector collection in the database.
	ColVersionVectors = "versionvectors"
	// ColWebhookLogs represents the webhook logs collection in the database.
	ColWebhookLogs = "webhooklogs"
)

// Collections represents the list of all collections in the database.
var Collections = []string{
	ColClusterNodes,
	ColProjects,
	ColUsers,
	ColClients,
	ColDocuments,
	ColSchemas,
	ColChanges,
	ColSnapshots,
	ColVersionVectors,
	ColWebhookLogs,
}

type collectionInfo struct {
	name    string
	indexes []mongo.IndexModel
}

// Below are names and indexes information of Collections that stores Yorkie data.
var collectionInfos = []collectionInfo{
	{
		name: ColClusterNodes,
		indexes: []mongo.IndexModel{
			{
				Keys:    bson.D{{Key: "rpc_addr", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
			{
				Keys: bson.D{{Key: "is_leader", Value: 1}},
				Options: options.Index().
					SetUnique(true).
					SetPartialFilterExpression(bson.M{"is_leader": true}).
					SetName("is_leader_true_unique"),
			},
			{
				Keys: bson.D{{Key: "updated_at", Value: 1}},
				Options: options.Index().
					SetExpireAfterSeconds(10).
					SetName("ttl_updated_at"),
			},
		},
	},
	{
		name: ColUsers,
		indexes: []mongo.IndexModel{{
			Keys:    bson.D{{Key: "username", Value: int32(1)}},
			Options: options.Index().SetUnique(true),
		}},
	},
	{
		name: ColProjects,
		indexes: []mongo.IndexModel{{
			Keys: bson.D{
				{Key: "owner", Value: int32(1)},
				{Key: "name", Value: int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}, {
			Keys:    bson.D{{Key: "public_key", Value: int32(1)}},
			Options: options.Index().SetUnique(true),
		}, {
			Keys:    bson.D{{Key: "secret_key", Value: int32(1)}},
			Options: options.Index().SetUnique(true),
		}},
	},
	{
		name: ColClients,
		indexes: []mongo.IndexModel{{
			Keys: bson.D{
				{Key: "project_id", Value: int32(1)}, // shard key
				{Key: "key", Value: int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}, {
			Keys: bson.D{
				{Key: "project_id", Value: int32(1)}, // shard key
				{Key: "status", Value: int32(1)},
				{Key: "updated_at", Value: int32(1)},
			},
		}, {
			Keys: bson.D{
				{Key: "project_id", Value: int32(1)}, // shard key
				{Key: "attached_docs", Value: int32(1)},
			},
		}, {
			// NOTE(hackerwins): This index is for deactivating clients efficiently.
			// But it skips the shard key to cover all clients in all projects.
			// We should monitor performance because this may cause scatter-gather.
			Keys: bson.D{
				{Key: "status", Value: int32(1)},
				{Key: "_id", Value: int32(1)},
			},
		}},
	},
	{
		name: ColDocuments,
		indexes: []mongo.IndexModel{{
			Keys: bson.D{
				{Key: "project_id", Value: int32(1)}, // shard key
				{Key: "key", Value: int32(1)},
				{Key: "removed_at", Value: int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}},
	},
	{
		name: ColSchemas,
		indexes: []mongo.IndexModel{{
			Keys: bson.D{
				{Key: "project_id", Value: int32(1)}, // shard key
				{Key: "name", Value: int32(1)},
				{Key: "version", Value: int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}},
	},
	{
		name: ColChanges,
		indexes: []mongo.IndexModel{{
			Keys: bson.D{
				{Key: "doc_id", Value: int32(1)}, // shard key
				{Key: "project_id", Value: int32(1)},
				{Key: "server_seq", Value: int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}, {
			Keys: bson.D{
				{Key: "doc_id", Value: int32(1)}, // shard key
				{Key: "project_id", Value: int32(1)},
				{Key: "actor_id", Value: int32(1)},
				{Key: "server_seq", Value: int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}},
	}, {
		name: ColSnapshots,
		indexes: []mongo.IndexModel{{
			Keys: bson.D{
				{Key: "doc_id", Value: int32(1)}, // shard key
				{Key: "project_id", Value: int32(1)},
				{Key: "server_seq", Value: int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}},
	}, {
		name: ColVersionVectors,
		indexes: []mongo.IndexModel{{
			Keys: bson.D{
				{Key: "doc_id", Value: int32(1)}, // shard key
				{Key: "project_id", Value: int32(1)},
				{Key: "client_id", Value: int32(1)},
			},
			Options: options.Index().SetUnique(true),
		}},
	}, {
		name: ColWebhookLogs,
		indexes: []mongo.IndexModel{{
			Keys: bson.D{
				{Key: "project_id", Value: int32(1)}, // shard key
				{Key: "webhook_type", Value: int32(1)},
				{Key: "created_at", Value: int32(-1)}, // desc
			},
		}, {
			Keys: bson.D{
				{Key: "created_at", Value: int32(1)},
			},
			Options: options.Index().SetExpireAfterSeconds(2592000), // TTL: 30day
		}},
	},
}

func ensureIndexes(ctx context.Context, db *mongo.Database) error {
	for _, info := range collectionInfos {
		_, err := db.Collection(info.name).Indexes().CreateMany(ctx, info.indexes)
		if err != nil {
			return fmt.Errorf("create indexes for %s: %w", info.name, err)
		}
	}
	return nil
}

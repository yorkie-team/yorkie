/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

// Below are names and indexes information of collections that stores Yorkie data.
var (
	ColClients     = "clients"
	idxClientInfos = []mongo.IndexModel{{
		Keys:    bsonx.Doc{{Key: "key", Value: bsonx.Int32(1)}},
		Options: options.Index().SetUnique(true),
	}}

	ColDocuments = "documents"
	idxDocInfos  = []mongo.IndexModel{{
		Keys:    bsonx.Doc{{Key: "key", Value: bsonx.Int32(1)}},
		Options: options.Index().SetUnique(true),
	}}

	ColChanges = "changes"
	idxChanges = []mongo.IndexModel{{
		Keys: bsonx.Doc{
			{Key: "doc_id", Value: bsonx.Int32(1)},
			{Key: "server_seq", Value: bsonx.Int32(1)},
		},
		Options: options.Index().SetUnique(true),
	}}

	ColSnapshots = "snapshots"
	idxSnapshots = []mongo.IndexModel{{
		Keys: bsonx.Doc{
			{Key: "doc_id", Value: bsonx.Int32(1)},
			{Key: "server_seq", Value: bsonx.Int32(1)},
		},
		Options: options.Index().SetUnique(true),
	}}

	ColSyncedSeqs = "syncedseqs"
	idxSyncedSeqs = []mongo.IndexModel{{
		Keys: bsonx.Doc{
			{Key: "doc_id", Value: bsonx.Int32(1)},
			{Key: "client_id", Value: bsonx.Int32(1)},
		},
		Options: options.Index().SetUnique(true),
	}, {
		Keys: bsonx.Doc{
			{Key: "doc_id", Value: bsonx.Int32(1)},
			{Key: "server_seq", Value: bsonx.Int32(1)},
		},
	}}
)

func ensureIndexes(ctx context.Context, db *mongo.Database) error {
	if _, err := db.Collection(ColClients).Indexes().CreateMany(
		ctx,
		idxClientInfos,
	); err != nil {
		return errors.WithStack(err)
	}

	if _, err := db.Collection(ColDocuments).Indexes().CreateMany(
		ctx,
		idxDocInfos,
	); err != nil {
		return errors.WithStack(err)
	}

	if _, err := db.Collection(ColChanges).Indexes().CreateMany(
		ctx,
		idxChanges,
	); err != nil {
		return errors.WithStack(err)
	}

	if _, err := db.Collection(ColSnapshots).Indexes().CreateMany(
		ctx,
		idxSnapshots,
	); err != nil {
		return errors.WithStack(err)
	}

	if _, err := db.Collection(ColSyncedSeqs).Indexes().CreateMany(
		ctx,
		idxSyncedSeqs,
	); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

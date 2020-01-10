package mongo

import (
	"context"

	"github.com/hackerwins/yorkie/pkg/log"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

var (
	ColClientInfos = "clients"
	idxClientInfos = []mongo.IndexModel{{
		Keys:    bsonx.Doc{{Key: "key", Value: bsonx.Int32(1)}},
		Options: options.Index().SetUnique(true),
	}}

	ColDocInfos = "documents"
	idxDocInfos = []mongo.IndexModel{{
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
)

func ensureIndexes(ctx context.Context, db *mongo.Database) error {
	if _, err := db.Collection(ColClientInfos).Indexes().CreateMany(
		ctx,
		idxClientInfos,
	); err != nil {
		log.Logger.Error(err)
		return err
	}

	if _, err := db.Collection(ColDocInfos).Indexes().CreateMany(
		ctx,
		idxDocInfos,
	); err != nil {
		log.Logger.Error(err)
		return err
	}

	if _, err := db.Collection(ColChanges).Indexes().CreateMany(
		ctx,
		idxChanges,
	); err != nil {
		log.Logger.Error(err)
		return err
	}

	return nil
}

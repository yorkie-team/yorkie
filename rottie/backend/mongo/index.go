package mongo

import (
	"context"

	"github.com/hackerwins/rottie/pkg/log"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

var (
	ColClientInfos = "clients"
	idxClientInfos = []mongo.IndexModel{{
		Keys:    bsonx.Doc{{"key", bsonx.Int32(1)}},
		Options: options.Index().SetUnique(true),
	}}

	ColDocInfos = "documents"
	idxDocInfos = []mongo.IndexModel{{
		Keys:    bsonx.Doc{{"key", bsonx.Int32(1)}},
		Options: options.Index().SetUnique(true),
	}}

	ColChanges = "changes"
	idxChanges = []mongo.IndexModel{{
		Keys: bsonx.Doc{
			{"doc_id", bsonx.Int32(1)},
			{"server_seq", bsonx.Int32(1)},
		},
		Options: options.Index().SetUnique(true),
	}}
)

func ensureIndex(ctx context.Context, db *mongo.Database) error {
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

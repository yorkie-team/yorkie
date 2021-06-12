package mongo

import (
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

type decoder interface {
	Decode(val interface{}) error
}

func decodeClientInfo(
	result decoder,
	clientInfo *db.ClientInfo,
) error {
	idHolder := struct {
		ID primitive.ObjectID `bson:"_id"`
	}{}
	if err := result.Decode(&idHolder); err != nil {
		return errors.WithStack(err)
	}
	if err := result.Decode(clientInfo); err != nil {
		return errors.WithStack(err)
	}
	clientInfo.ID = decodeID(idHolder.ID)
	return nil
}

func decodeDocInfo(
	result decoder,
	docInfo *db.DocInfo,
) error {
	idHolder := struct {
		ID    primitive.ObjectID `bson:"_id"`
		Owner primitive.ObjectID `bson:"owner"`
	}{}
	if err := result.Decode(&idHolder); err != nil {
		return errors.WithStack(err)
	}
	if err := result.Decode(&docInfo); err != nil {
		return errors.WithStack(err)
	}
	docInfo.ID = decodeID(idHolder.ID)
	docInfo.Owner = decodeID(idHolder.Owner)
	return nil
}

func decodeChangeInfo(
	cursor decoder,
	changeInfo *db.ChangeInfo,
) error {
	idHolder := struct {
		DocID primitive.ObjectID `bson:"doc_id"`
		Actor primitive.ObjectID `bson:"actor"`
	}{}
	if err := cursor.Decode(&idHolder); err != nil {
		return errors.WithStack(err)
	}
	if err := cursor.Decode(&changeInfo); err != nil {
		return errors.WithStack(err)
	}
	changeInfo.DocID = decodeID(idHolder.DocID)
	changeInfo.Actor = decodeID(idHolder.Actor)
	return nil
}

func decodeSyncedSeqInfo(
	result decoder,
	syncedSeqInfo *db.SyncedSeqInfo,
) error {
	idHolder := struct {
		DocID    primitive.ObjectID `bson:"doc_id"`
		ClientID primitive.ObjectID `bson:"client_id"`
	}{}
	if err := result.Decode(&idHolder); err != nil {
		return errors.WithStack(err)
	}
	if err := result.Decode(&syncedSeqInfo); err != nil {
		return errors.WithStack(err)
	}
	syncedSeqInfo.DocID = decodeID(idHolder.DocID)
	syncedSeqInfo.ClientID = decodeID(idHolder.ClientID)
	return nil
}

func decodeSnapshotInfo(
	result decoder,
	snapshotInfo *db.SnapshotInfo,
) error {
	idHolder := struct {
		ID    primitive.ObjectID `bson:"_id"`
		DocID primitive.ObjectID `bson:"doc_id"`
	}{}
	if err := result.Decode(&idHolder); err != nil {
		return errors.WithStack(err)
	}
	if err := result.Decode(&snapshotInfo); err != nil {
		return errors.WithStack(err)
	}
	snapshotInfo.ID = decodeID(idHolder.ID)
	snapshotInfo.DocID = decodeID(idHolder.DocID)
	return nil
}

func decodeID(id primitive.ObjectID) db.ID {
	return db.ID(id.Hex())
}

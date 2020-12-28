package mongo

import (
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

type decoder interface {
	Decode(val interface{}) error
}

func (c *Client) decodeClientInfo(
	result decoder,
	clientInfo *db.ClientInfo,
) error {
	idHolder := struct {
		ID primitive.ObjectID `bson:"_id"`
	}{}
	if err := result.Decode(&idHolder); err != nil {
		log.Logger.Error(err)
		return err
	}
	if err := result.Decode(clientInfo); err != nil {
		log.Logger.Error(err)
		return err
	}
	clientInfo.ID = c.decodeID(idHolder.ID)
	return nil
}

func (c *Client) decodeDocInfo(
	result decoder,
	docInfo *db.DocInfo,
) error {
	idHolder := struct {
		ID    primitive.ObjectID `bson:"_id"`
		Owner primitive.ObjectID `bson:"owner"`
	}{}
	if err := result.Decode(&idHolder); err != nil {
		log.Logger.Error(err)
		return err
	}
	if err := result.Decode(&docInfo); err != nil {
		log.Logger.Error(err)
		return err
	}
	docInfo.ID = c.decodeID(idHolder.ID)
	docInfo.Owner = c.decodeID(idHolder.Owner)
	return nil
}

func (c *Client) decodeChangeInfo(
	cursor decoder,
	changeInfo *db.ChangeInfo,
) error {
	idHolder := struct {
		DocID primitive.ObjectID `bson:"doc_id"`
		Actor primitive.ObjectID `bson:"actor"`
	}{}
	if err := cursor.Decode(&idHolder); err != nil {
		log.Logger.Error(err)
		return err
	}
	if err := cursor.Decode(&changeInfo); err != nil {
		log.Logger.Error(err)
		return err
	}
	changeInfo.DocID = c.decodeID(idHolder.DocID)
	changeInfo.Actor = c.decodeID(idHolder.Actor)
	return nil
}

func (c *Client) decodeSyncedSeqInfo(
	result decoder,
	syncedSeqInfo *db.SyncedSeqInfo,
) error {
	idHolder := struct {
		DocID    primitive.ObjectID `bson:"doc_id"`
		ClientID primitive.ObjectID `bson:"client_id"`
	}{}
	if err := result.Decode(&idHolder); err != nil {
		log.Logger.Error(err)
		return err
	}
	if err := result.Decode(&syncedSeqInfo); err != nil {
		log.Logger.Error(err)
		return err
	}
	syncedSeqInfo.DocID = c.decodeID(idHolder.DocID)
	syncedSeqInfo.ClientID = c.decodeID(idHolder.ClientID)
	return nil
}

func (c *Client) decodeSnapshotInfo(
	result decoder,
	snapshotInfo *db.SnapshotInfo,
) error {
	idHolder := struct {
		ID    primitive.ObjectID `bson:"_id"`
		DocID primitive.ObjectID `bson:"doc_id"`
	}{}
	if err := result.Decode(&idHolder); err != nil {
		log.Logger.Error(err)
		return err
	}
	if err := result.Decode(&snapshotInfo); err != nil {
		log.Logger.Error(err)
		return err
	}
	snapshotInfo.ID = c.decodeID(idHolder.ID)
	snapshotInfo.DocID = c.decodeID(idHolder.DocID)
	return nil
}

func (c *Client) decodeID(id primitive.ObjectID) db.ID {
	return db.ID(id.Hex())
}

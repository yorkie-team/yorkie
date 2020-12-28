package mongo

import (
	"fmt"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

func (c *Client) encodeActorID(id *time.ActorID) primitive.ObjectID {
	objectID := primitive.ObjectID{}
	copy(objectID[:], id[:])
	return objectID
}

func (c *Client) encodeID(id db.ID) (primitive.ObjectID, error) {
	objectID, err := primitive.ObjectIDFromHex(string(id))
	if err != nil {
		log.Logger.Error(err)
		return objectID, fmt.Errorf("%s: %w", id, db.ErrInvalidID)
	}
	return objectID, nil
}

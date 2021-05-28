package mongo

import (
	"fmt"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

func encodeActorID(id *time.ActorID) primitive.ObjectID {
	objectID := primitive.ObjectID{}
	copy(objectID[:], id[:])
	return objectID
}

func encodeID(id db.ID) (primitive.ObjectID, error) {
	objectID, err := primitive.ObjectIDFromHex(id.String())
	if err != nil {
		log.Logger.Error(err)
		return objectID, fmt.Errorf("%s: %w", id, db.ErrInvalidID)
	}
	return objectID, nil
}

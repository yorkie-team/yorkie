package types

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type SnapshotInfo struct {
	ID        primitive.ObjectID `bson:"_id"`
	DocID     primitive.ObjectID `bson:"doc_id"`
	ServerSeq uint64             `bson:"server_seq"`
	Snapshot  []byte             `bson:"snapshot"`
	CreatedAt time.Time          `bson:"created_at"`
}

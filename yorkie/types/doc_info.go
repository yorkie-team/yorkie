package types

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type DocInfo struct {
	ID         primitive.ObjectID `bson:"_id"`
	Key        string             `bson:"key"`
	ServerSeq  uint64             `bson:"server_seq"`
	Owner      primitive.ObjectID `bson:"owner"`
	CreatedAt  time.Time          `bson:"created_at"`
	AccessedAt time.Time          `bson:"accessed_at"`
	UpdatedAt  time.Time          `bson:"updated_at"`
}

func (info *DocInfo) IncreaseServerSeq() uint64 {
	info.ServerSeq++
	return info.ServerSeq
}

package types

import "go.mongodb.org/mongo-driver/bson/primitive"

type SyncedSeqInfo struct {
	DocID     primitive.ObjectID `bson:"doc_id"`
	ClientID  primitive.ObjectID `bson:"client_id"`
	ServerSeq uint64             `bson:"server_seq"`
}

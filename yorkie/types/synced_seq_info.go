package types

import "go.mongodb.org/mongo-driver/bson/primitive"

// SyncedSeqInfo is a structure representing information about the synchronized
// sequence for each client.
type SyncedSeqInfo struct {
	DocID     primitive.ObjectID `bson:"doc_id"`
	ClientID  primitive.ObjectID `bson:"client_id"`
	ServerSeq uint64             `bson:"server_seq"`
}

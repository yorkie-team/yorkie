package db

import (
	"time"
)

// SnapshotInfo is a structure representing information of the snapshot.
type SnapshotInfo struct {
	ID        ID        `bson:"_id"`
	DocID     ID        `bson:"doc_id"`
	ServerSeq uint64    `bson:"server_seq"`
	Snapshot  []byte    `bson:"snapshot"`
	CreatedAt time.Time `bson:"created_at"`
}

package db

// SyncedSeqInfo is a structure representing information about the synchronized
// sequence for each client.
type SyncedSeqInfo struct {
	DocID     ID     `bson:"doc_id"`
	ClientID  ID     `bson:"client_id"`
	ServerSeq uint64 `bson:"server_seq"`
}

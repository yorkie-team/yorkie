package db

// SyncedSeqInfo is a structure representing information about the synchronized
// sequence for each client.
type SyncedSeqInfo struct {
	DocID     ID     `bson:"doc_id_fake"`
	ClientID  ID     `bson:"client_id_fake"`
	ServerSeq uint64 `bson:"server_seq"`
}

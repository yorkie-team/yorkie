package scylla

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/gocql/gocql"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

var (
	//tblClusterNodes   = "clusternodes"
	//tblUsers          = "users"
	//tblProjects       = "projects"
	//tblMembers        = "members"
	//tblInvites        = "invites"
	//tblClients        = "clients"
	//tblDocuments      = "documents"
	//tblSchemas        = "schemas"
	tblChanges        = "changes"
	tblSnapshots      = "snapshots"
	tblVersionVectors = "versionvectors"
	//tblRevisions      = "revisions"
)

type columnInfo struct {
	name       string
	columnType string
}

type TableDef struct {
	name         string
	columns      []columnInfo
	partitionKey []string
	clustringKey []string
	options      []string
}

var tableDefs = []TableDef{
	{
		name: tblChanges,
		columns: []columnInfo{
			{"_id", "text"},                         // types.ID
			{"project_id", "text"},                  // types.ID
			{"doc_id", "text"},                      // types.ID
			{"server_seq", "bigint"},                // int64
			{"client_seq", "int"},                   // uint32
			{"lamport", "bigint"},                   // int64
			{"actor_id", "text"},                    // types.ID
			{"version_vector", "map<text, bigint>"}, // time.VersionVector
			{"message", "text"},                     // string
			{"operations", "blob"},                  // [][]byte
			{"presence", "blob"},                    // *presence.Change
		},
		partitionKey: []string{
			"project_id",
			"doc_id",
		},
		clustringKey: []string{
			"server_seq",
		},
	},
	{
		name: tblSnapshots,
		columns: []columnInfo{
			{"_id", "text"},                         // types.ID
			{"project_id", "text"},                  // types.ID
			{"doc_id", "text"},                      // types.ID
			{"server_seq", "bigint"},                // int64
			{"lamport", "bigint"},                   // int64
			{"version_vector", "map<text, bigint>"}, // time.VersionVector
			{"snapshot", "blob"},                    // []byte
			{"created_at", "timestamp"},             // gotime.Time
		},
		partitionKey: []string{
			"project_id",
			"doc_id",
		},
		clustringKey: []string{
			"server_seq",
		},
		options: []string{
			"CLUSTERING ORDER BY (server_seq ASC)",
		},
	},
	{
		name: tblVersionVectors,
		columns: []columnInfo{
			{"_id", "text"},                         // types.ID
			{"project_id", "text"},                  // types.ID
			{"doc_id", "text"},                      // types.ID
			{"client_id", "text"},                   // types.ID
			{"version_vector", "map<text, bigint>"}, // time.VersionVector
			{"server_seq", "bigint"},                // int64
		},
		partitionKey: []string{
			"project_id",
			"doc_id",
		},
		clustringKey: []string{
			"client_id",
		},
	},
}

func buildCreateTableCQL(def TableDef, keyspace string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (",
		keyspace, def.name))

	// columns
	for i, col := range def.columns {
		sb.WriteString(fmt.Sprintf("\"%s\" %s", col.name, col.columnType))
		if i < len(def.columns)-1 {
			sb.WriteString(", ")
		}
	}

	// PRIMARY KEY
	sb.WriteString(", PRIMARY KEY ((")
	sb.WriteString(strings.Join(def.partitionKey, ", "))
	sb.WriteString(")")

	if len(def.clustringKey) > 0 {
		sb.WriteString(", ")
		sb.WriteString(strings.Join(def.clustringKey, ", "))
	}

	sb.WriteString("))")

	// options
	if len(def.options) > 0 {
		sb.WriteString(" WITH ")
		sb.WriteString(strings.Join(def.options, " AND "))
	}

	sb.WriteString(";")

	return sb.String()
}

func createTables(ctx context.Context, s *gocql.Session, keyspace string) error {
	for _, def := range tableDefs {
		cql := buildCreateTableCQL(def, keyspace)
		if err := s.Query(cql).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("create table %s: %w", cql, err)
		}
	}

	return nil
}

func FromScyllaVersionVector(m map[string]int64) time.VersionVector {
	out := make(time.VersionVector)
	for k, v := range m {
		bytes, _ := hex.DecodeString(k)
		var id time.ActorID
		copy(id[:], bytes)
		out[id] = v
	}
	return out
}

func ToScyllaVersionVector(vv time.VersionVector) map[string]int64 {
	out := make(map[string]int64, len(vv))
	for k, v := range vv {
		out[hex.EncodeToString(k[:])] = v
	}
	return out
}

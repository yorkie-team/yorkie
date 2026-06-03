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
	tblClients         = "clients"
	tblClientDocuments = "client_documents"
	tblDocClients      = "doc_clients"
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
	group        tableGroup
	columns      []columnInfo
	partitionKey []string
	clustringKey []string
	options      []string
}

type tableGroup int

const (
	groupClients tableGroup = iota
	groupChanges
	groupSnapshots
	groupVersionVectors
)

func (g tableGroup) enabled(t Tables) bool {
	switch g {
	case groupClients:
		return t.Clients
	case groupChanges:
		return t.Changes
	case groupSnapshots:
		return t.Snapshots
	case groupVersionVectors:
		return t.VersionVectors
	default:
		return false
	}
}

var tableDefs = []TableDef{
	{
		name:  tblClients,
		group: groupClients,
		columns: []columnInfo{
			{"_id", "text"},
			{"project_id", "text"},
			{"key", "text"},
			{"status", "text"},
			{"metadata", "map<text, text>"},
			{"created_at", "timestamp"},
			{"updated_at", "timestamp"},
		},
		partitionKey: []string{"project_id"},
		clustringKey: []string{"\"_id\""},
		options: []string{
			"CLUSTERING ORDER BY (\"_id\" ASC)",
		},
	},
	{
		name:  tblClientDocuments,
		group: groupClients,
		columns: []columnInfo{
			{"project_id", "text"},
			{"client_id", "text"},
			{"doc_id", "text"},
			{"status", "text"},
			{"server_seq", "bigint"},
			{"client_seq", "int"},
		},
		partitionKey: []string{"project_id", "client_id"},
		clustringKey: []string{"doc_id"},
	},
	{
		name:  tblDocClients,
		group: groupClients,
		columns: []columnInfo{
			{"project_id", "text"},
			{"doc_id", "text"},
			{"client_id", "text"},
			{"doc_status", "text"},
		},
		partitionKey: []string{"project_id", "doc_id"},
		clustringKey: []string{"client_id"},
	},
	{
		name:  tblChanges,
		group: groupChanges,
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
		name:  tblSnapshots,
		group: groupSnapshots,
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
		name:  tblVersionVectors,
		group: groupVersionVectors,
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

func createTables(ctx context.Context, s *gocql.Session, keyspace string, enabled Tables) error {
	for _, def := range tableDefs {
		if !def.group.enabled(enabled) {
			continue
		}
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

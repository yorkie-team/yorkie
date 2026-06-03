/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package scylla provides a write-heavy storage layer backed by ScyllaDB.
// See docs/design/scylladb-storage.md for the gradual-adoption design.
package scylla

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"github.com/gocql/gocql"
)

// Tables selects which logical table groups are stored in ScyllaDB. Each flag
// is independent; methods that touch a disabled group are delegated to MongoDB.
// See docs/design/scylladb-storage.md.
type Tables struct {
	// Clients groups: clients, client_documents, doc_clients.
	Clients bool
	// Changes group: changes.
	Changes bool
	// Snapshots group: snapshots.
	Snapshots bool
	// VersionVectors group: versionvectors.
	VersionVectors bool
}

// Any reports whether at least one table group is enabled on ScyllaDB.
func (t Tables) Any() bool {
	return t.Clients || t.Changes || t.Snapshots || t.VersionVectors
}

// All reports whether every table group is enabled on ScyllaDB.
func (t Tables) All() bool {
	return t.Clients && t.Changes && t.Snapshots && t.VersionVectors
}

// Config is the ScyllaDB connection and dispatch configuration.
type Config struct {
	Hosts []string // ["127.0.0.1"]
	Port  int      // 9042

	Keyspace string

	Username string
	Password string

	Consistency string // "LOCAL_QUORUM", "ONE"

	ConnectTimeout string // "5s"
	QueryTimeout   string // "3s"

	MonitoringEnabled            bool
	MonitoringSlowQueryThreshold string // "200ms"

	// ReplicationFactor is the SimpleStrategy replication factor used when the
	// keyspace is (re)created on Dial. Set this to match the number of nodes
	// in your ScyllaDB cluster. RF=1 is appropriate for single-node dev/test
	// instances; RF should typically equal the cluster size in perf-testing
	// setups so a sharded MongoDB and a sharded ScyllaDB cluster are compared
	// on equal footing.
	ReplicationFactor int

	// Tables selects which logical table groups are stored in ScyllaDB. The
	// remaining groups are transparently delegated to the MongoDB client. This
	// is the dispatcher used for gradual adoption / partial-migration perf
	// experiments — see docs/design/scylladb-storage.md.
	Tables Tables
}

func (c *Config) parseConsistency() gocql.Consistency {
	consistency, err := gocql.ParseConsistencyWrapper(c.Consistency)
	if err != nil {
		return gocql.LocalQuorum
	}
	return consistency
}

// RandomKeyspace returns a randomized lowercase keyspace name suitable for
// test isolation. Lowercase only because ScyllaDB folds unquoted identifiers
// to lowercase on CREATE — mixing cases breaks the subsequent USE.
func RandomKeyspace() string {
	letters := []byte("abcdefghijklmnopqrstuvwxyz")
	result := make([]byte, 8)
	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			return "yorkie_test"
		}
		result[i] = letters[num.Int64()]
	}
	return string(result)
}

// GetScyllaConfig returns a default ScyllaDB config pointing at a local
// single-node instance with all table groups enabled. Callers typically
// override Tables and/or Keyspace before passing it to Dial.
func GetScyllaConfig() *Config {
	return &Config{
		Hosts:                        []string{"127.0.0.1"},
		Port:                         9042,
		Keyspace:                     "yorkie",
		Username:                     "",
		Password:                     "",
		Consistency:                  "ONE",
		ConnectTimeout:               "5s",
		QueryTimeout:                 "3s",
		MonitoringEnabled:            false,
		MonitoringSlowQueryThreshold: "200ms",
		ReplicationFactor:            1,
		Tables: Tables{
			Clients:        true,
			Changes:        true,
			Snapshots:      true,
			VersionVectors: true,
		},
	}
}

// ScyllaDBInfo returns a human-readable connection string for logging.
func (c *Config) ScyllaDBInfo() string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf(
		"scylla://%s/%s",
		strings.Join(c.Hosts, ","),
		c.Keyspace,
	)
}

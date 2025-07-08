/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package memory

import "github.com/hashicorp/go-memdb"

var (
	tblProjects       = "projects"
	tblUsers          = "users"
	tblClients        = "clients"
	tblDocuments      = "documents"
	tblChanges        = "changes"
	tblSnapshots      = "snapshots"
	tblVersionVectors = "versionvectors"
	tblSchemas        = "schemas"
	tblLeaderships    = "leaderships"
)

var schema = &memdb.DBSchema{
	Tables: map[string]*memdb.TableSchema{
		tblProjects: {
			Name: tblProjects,
			Indexes: map[string]*memdb.IndexSchema{
				"id": {
					Name:    "id",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "ID"},
				},
				"owner_name": {
					Name:   "owner_name",
					Unique: true,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "Owner"},
							&memdb.StringFieldIndex{Field: "Name"},
						},
					},
				},
				"public_key": {
					Name:    "public_key",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "PublicKey"},
				},
				"secret_key": {
					Name:    "secret_key",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "SecretKey"},
				},
			},
		},
		tblUsers: {
			Name: tblUsers,
			Indexes: map[string]*memdb.IndexSchema{
				"id": {
					Name:    "id",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "ID"},
				},
				"username": {
					Name:    "username",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "Username"},
				},
			},
		},
		tblClients: {
			Name: tblClients,
			Indexes: map[string]*memdb.IndexSchema{
				"id": {
					Name:    "id",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "ID"},
				},
				"project_id": {
					Name:    "project_id",
					Indexer: &memdb.StringFieldIndex{Field: "ProjectID"},
				},
				"project_id_key": {
					Name:   "project_id_key",
					Unique: true,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "ProjectID"},
							&memdb.StringFieldIndex{Field: "Key"},
						},
					},
				},
				"project_id_status_updated_at": {
					Name: "project_id_status_updated_at",
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "ProjectID"},
							&memdb.StringFieldIndex{Field: "Status"},
							&memdb.TimeFieldIndex{Field: "UpdatedAt"},
						},
					},
				},
			},
		},
		tblDocuments: {
			Name: tblDocuments,
			Indexes: map[string]*memdb.IndexSchema{
				"id": {
					Name:    "id",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "ID"},
				},
				"project_id": {
					Name:    "project_id",
					Indexer: &memdb.StringFieldIndex{Field: "ProjectID"},
				},
				"project_id_id": {
					Name:   "project_id_id",
					Unique: true,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "ProjectID"},
							&memdb.StringFieldIndex{Field: "ID"},
						},
					},
				},
				"project_id_key": {
					Name: "project_id_key",
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "ProjectID"},
							&memdb.StringFieldIndex{Field: "Key"},
						},
					},
				},
				"project_id_key_removed_at": {
					Name: "project_id_key_removed_at",
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "ProjectID"},
							&memdb.StringFieldIndex{Field: "Key"},
							&memdb.TimeFieldIndex{Field: "RemovedAt"},
						},
					},
				},
			},
		},
		tblChanges: {
			Name: tblChanges,
			Indexes: map[string]*memdb.IndexSchema{
				"id": {
					Name:    "id",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "ID"},
				},
				"doc_id": {
					Name:    "doc_id",
					Unique:  false,
					Indexer: &memdb.StringFieldIndex{Field: "DocID"},
				},
				"doc_id_server_seq": {
					Name:   "doc_id_server_seq",
					Unique: true,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "DocID"},
							&memdb.IntFieldIndex{Field: "ServerSeq"},
						},
					},
				},
				"doc_id_actor_id_server_seq": {
					Name:   "doc_id_actor_id_server_seq",
					Unique: true,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "DocID"},
							&memdb.StringFieldIndex{Field: "ActorID"},
							&memdb.IntFieldIndex{Field: "ServerSeq"},
						},
					},
				},
			},
		},
		tblSnapshots: {
			Name: tblSnapshots,
			Indexes: map[string]*memdb.IndexSchema{
				"id": {
					Name:    "id",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "ID"},
				},
				"doc_id": {
					Name:    "doc_id",
					Unique:  false,
					Indexer: &memdb.StringFieldIndex{Field: "DocID"},
				},
				"doc_id_server_seq": {
					Name:   "doc_id_server_seq",
					Unique: true,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "DocID"},
							&memdb.IntFieldIndex{Field: "ServerSeq"},
						},
					},
				},
			},
		},
		tblVersionVectors: {
			Name: tblVersionVectors,
			Indexes: map[string]*memdb.IndexSchema{
				"id": {
					Name:    "id",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "ID"},
				},
				"doc_id": {
					Name:   "doc_id",
					Unique: false,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "DocID"},
						},
					},
				},
				"doc_id_client_id": {
					Name:   "doc_id_client_id",
					Unique: true,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "DocID"},
							&memdb.StringFieldIndex{Field: "ClientID"},
						},
					},
				},
				"doc_id_server_seq": {
					Name:   "doc_id_server_seq",
					Unique: true,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "DocID"},
							&memdb.IntFieldIndex{Field: "ServerSeq"},
						},
					},
				},
			},
		},
		tblSchemas: {
			Name: tblSchemas,
			Indexes: map[string]*memdb.IndexSchema{
				"id": {
					Name:    "id",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "ID"},
				},
				"project_id": {
					Name:    "project_id",
					Indexer: &memdb.StringFieldIndex{Field: "ProjectID"},
				},
				"project_id_name": {
					Name: "project_id_name",
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "ProjectID"},
							&memdb.StringFieldIndex{Field: "Name"},
						},
					},
				},
				"project_id_name_version": {
					Name:   "project_id_name_version",
					Unique: true,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "ProjectID"},
							&memdb.StringFieldIndex{Field: "Name"},
							&memdb.IntFieldIndex{Field: "Version"},
						},
					},
				},
			},
		},
		tblLeaderships: {
			Name: tblLeaderships,
			Indexes: map[string]*memdb.IndexSchema{
				"id": {
					Name:    "id",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "ID"},
				},
			},
		},
	},
}

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
	tblProjects   = "projects"
	tblUsers      = "users"
	tblClients    = "clients"
	tblDocuments  = "documents"
	tblChanges    = "changes"
	tblSnapshots  = "snapshots"
	tblSyncedSeqs = "syncedseqs"
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
				"name": {
					Name:    "name",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "Name"},
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
				"email": {
					Name:    "email",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "Email"},
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
				"status_updated_at": {
					Name: "status_updated_at",
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
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
					Name:   "project_id_key",
					Unique: true,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "ProjectID"},
							&memdb.StringFieldIndex{Field: "Key"},
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
				"doc_id_server_seq": {
					Name:   "doc_id_server_seq",
					Unique: true,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "DocID"},
							&memdb.UintFieldIndex{Field: "ServerSeq"},
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
				"doc_id_server_seq": {
					Name:   "doc_id_server_seq",
					Unique: true,
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "DocID"},
							&memdb.UintFieldIndex{Field: "ServerSeq"},
						},
					},
				},
			},
		},
		tblSyncedSeqs: {
			Name: tblSyncedSeqs,
			Indexes: map[string]*memdb.IndexSchema{
				"id": {
					Name:    "id",
					Unique:  true,
					Indexer: &memdb.StringFieldIndex{Field: "ID"},
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
				"doc_id_lamport_actor_id": {
					Name: "doc_id_lamport_actor_id",
					Indexer: &memdb.CompoundIndex{
						Indexes: []memdb.Indexer{
							&memdb.StringFieldIndex{Field: "DocID"},
							&memdb.UintFieldIndex{Field: "Lamport"},
							&memdb.StringFieldIndex{Field: "ActorID"},
						},
					},
				},
			},
		},
	},
}

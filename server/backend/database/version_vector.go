/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

package database

import (
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// VersionVectorInfo is a structure representing information about the version vector for each document and client.
type VersionVectorInfo struct {
	ID            types.ID           `bson:"_id"`
	ProjectID     types.ID           `bson:"project_id"`
	DocID         types.ID           `bson:"doc_id"`
	ClientID      types.ID           `bson:"client_id"`
	VersionVector time.VersionVector `bson:"version_vector"`
	ServerSeq     int64              `bson:"server_seq"`
}

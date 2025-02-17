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
}

// FindMinVersionVector finds the minimum version vector from the given version vector infos.
// It excludes the version vector of the given client ID if specified.
func FindMinVersionVector(vvInfos []VersionVectorInfo, excludeClientID types.ID) time.VersionVector {
	var minVV time.VersionVector

	for _, vvi := range vvInfos {
		if vvi.ClientID == excludeClientID {
			continue
		}

		if minVV == nil {
			minVV = vvi.VersionVector.DeepCopy()
			continue
		}

		for actorID, lamport := range vvi.VersionVector {
			if currentLamport, exists := minVV[actorID]; !exists {
				minVV[actorID] = 0
			} else if lamport < currentLamport {
				minVV[actorID] = lamport
			}
		}

		for actorID := range minVV {
			if _, exists := vvi.VersionVector[actorID]; !exists {
				minVV[actorID] = 0
			}
		}
	}

	return minVV
}

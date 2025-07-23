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

package mongo

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func encodeActorID(id time.ActorID) bson.ObjectID {
	objectID := bson.ObjectID{}
	copy(objectID[:], id.Bytes())
	return objectID
}

func encodeID(id types.ID) (bson.ObjectID, error) {
	objectID, err := bson.ObjectIDFromHex(id.String())
	if err != nil {
		return objectID, fmt.Errorf("%s: %w", id, types.ErrInvalidID)
	}
	return objectID, nil
}

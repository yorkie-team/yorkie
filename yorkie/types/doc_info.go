/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package types

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/yorkie-team/yorkie/pkg/document/key"
)

// DocInfo is a structure representing information of the document.
type DocInfo struct {
	ID         primitive.ObjectID `bson:"_id"`
	Key        string             `bson:"key"`
	ServerSeq  uint64             `bson:"server_seq"`
	Owner      primitive.ObjectID `bson:"owner"`
	CreatedAt  time.Time          `bson:"created_at"`
	AccessedAt time.Time          `bson:"accessed_at"`
	UpdatedAt  time.Time          `bson:"updated_at"`
}

// IncreaseServerSeq increases server sequence of the document.
func (info *DocInfo) IncreaseServerSeq() uint64 {
	info.ServerSeq++
	return info.ServerSeq
}

// GetKey creates Key instance of this DocInfo.
func (info *DocInfo) GetKey() (*key.Key, error) {
	docKey, err := key.FromBSONKey(info.Key)
	if err != nil {
		return nil, err
	}

	return docKey, nil
}

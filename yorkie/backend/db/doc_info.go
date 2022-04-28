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

package db

import (
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
)

// DocInfo is a structure representing information of the document.
type DocInfo struct {
	ID          types.ID  `bson:"_id"`
	CombinedKey string    `bson:"key"`
	ServerSeq   uint64    `bson:"server_seq"`
	Owner       types.ID  `bson:"owner"`
	CreatedAt   time.Time `bson:"created_at"`
	AccessedAt  time.Time `bson:"accessed_at"`
	UpdatedAt   time.Time `bson:"updated_at"`
}

// IncreaseServerSeq increases server sequence of the document.
func (info *DocInfo) IncreaseServerSeq() uint64 {
	info.ServerSeq++
	return info.ServerSeq
}

// Key creates Key instance of this DocInfo.
func (info *DocInfo) Key() (key.Key, error) {
	docKey, err := key.FromCombinedKey(info.CombinedKey)
	if err != nil {
		return key.Key{}, err
	}

	return docKey, nil
}

// DeepCopy creates a deep copy of this DocInfo.
func (info *DocInfo) DeepCopy() *DocInfo {
	if info == nil {
		return nil
	}

	return &DocInfo{
		ID:          info.ID,
		CombinedKey: info.CombinedKey,
		ServerSeq:   info.ServerSeq,
		Owner:       info.Owner,
		CreatedAt:   info.CreatedAt,
		AccessedAt:  info.AccessedAt,
		UpdatedAt:   info.UpdatedAt,
	}
}

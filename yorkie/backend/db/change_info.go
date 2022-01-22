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
	"errors"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/yorkie/log"
)

// ErrEncodeOperationFailed is returned when encoding operation failed.
var ErrEncodeOperationFailed = errors.New("encode operation failed")

// ChangeInfo is a structure representing information of a change.
type ChangeInfo struct {
	ID         ID       `bson:"_id"`
	DocID      ID       `bson:"doc_id"`
	ServerSeq  uint64   `bson:"server_seq"`
	ClientSeq  uint32   `bson:"client_seq"`
	Lamport    uint64   `bson:"lamport"`
	ActorID    ID       `bson:"actor_id"`
	Message    string   `bson:"message"`
	Operations [][]byte `bson:"operations"`
}

// EncodeOperations encodes the given operations into bytes array.
func EncodeOperations(operations []operation.Operation) ([][]byte, error) {
	var encodedOps [][]byte

	changes, err := converter.ToOperations(operations)
	if err != nil {
		return nil, err
	}

	for _, pbOp := range changes {
		encodedOp, err := pbOp.Marshal()
		if err != nil {
			log.Logger().Error(err)
			return nil, ErrEncodeOperationFailed
		}
		encodedOps = append(encodedOps, encodedOp)
	}

	return encodedOps, nil
}

// ToChange creates Change model from this ChangeInfo.
func (i *ChangeInfo) ToChange() (*change.Change, error) {
	actorID, err := time.ActorIDFromHex(i.ActorID.String())
	if err != nil {
		return nil, err
	}

	changeID := change.NewID(i.ClientSeq, i.Lamport, actorID)

	var pbOps []*api.Operation
	for _, bytesOp := range i.Operations {
		pbOp := api.Operation{}
		if err := pbOp.Unmarshal(bytesOp); err != nil {
			log.Logger().Error(err)
			return nil, err
		}
		pbOps = append(pbOps, &pbOp)
	}

	ops, err := converter.FromOperations(pbOps)
	if err != nil {
		return nil, err
	}

	c := change.New(changeID, i.Message, ops)
	c.SetServerSeq(i.ServerSeq)

	return c, nil
}
